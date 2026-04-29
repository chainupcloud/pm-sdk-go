package relayer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// Service 是 Client + DigestSigner 组合而成的高层 helper：
//
//   - DeploySafe        — 一次调用完成 SAFE-CREATE 全流程：digest → 签名 → /submit → 轮询终态。
//   - ExecuteSafeTx     — 一次调用完成 SAFE 全流程：GET /nonce → digest → 签名 → /submit → 轮询终态。
//
// 配套字段在 NewService 时一次性绑定：chainID、SafeProxyFactory 地址（DeploySafe
// 用作 SAFE-CREATE 的 to 与 EIP-712 verifyingContract）。其余每次调用动态参数从
// 方法入参传入。
type Service struct {
	client          *Client
	signer          DigestSigner
	chainID         int64
	safeFactory     common.Address
	pollInterval    time.Duration
	pollMaxAttempts int
}

// ServiceOption 是 NewService 选项。
type ServiceOption func(*Service)

// WithPollInterval 调整 /transaction 轮询间隔（默认 2s）。
func WithPollInterval(d time.Duration) ServiceOption {
	return func(s *Service) {
		if d > 0 {
			s.pollInterval = d
		}
	}
}

// WithPollMaxAttempts 调整 /transaction 轮询最大次数（默认 120 次 ≈ 4 分钟，
// 与 mm V2 一致）。超过返回 ErrTxTimeout。
func WithPollMaxAttempts(n int) ServiceOption {
	return func(s *Service) {
		if n > 0 {
			s.pollMaxAttempts = n
		}
	}
}

// NewService 构造 Service。signer 为 nil 时返回 ErrInvalidConfig；
// safeFactory 为零地址时返回 ErrInvalidConfig（DeploySafe 必需）。
func NewService(client *Client, signer DigestSigner, chainID int64, safeFactory common.Address, opts ...ServiceOption) (*Service, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: nil client", ErrInvalidConfig)
	}
	if signer == nil {
		return nil, fmt.Errorf("%w: nil signer", ErrInvalidConfig)
	}
	if (safeFactory == common.Address{}) {
		return nil, fmt.Errorf("%w: zero safeFactory address", ErrInvalidConfig)
	}
	s := &Service{
		client:          client,
		signer:          signer,
		chainID:         chainID,
		safeFactory:     safeFactory,
		pollInterval:    2 * time.Second,
		pollMaxAttempts: 120,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s, nil
}

// DeploySafe 走 SAFE-CREATE 路径，返回 (transactionID, safeAddress, error)。
//
// scopeID 必填（hex 字符串，0x 前缀可有可无；ParseScopeID 会左 padding 到 32 byte）。
// paymentToken/payment/paymentReceiver 默认全部传零值即可（mm V2 prod 用法）；
// 调用方需要付费 deploy 时再填非零。
//
// 流程：
//  1. GET /deployed → 拿到 CREATE2 预测的 safeAddress（同时检查是否已部署，已部署
//     直接返回 ("", safeAddress, nil)）
//  2. 计算 EIP-712 digest（factory + chainID 由 Service 持有）
//  3. signer.SignDigest 签名 + v 加 27
//  4. POST /submit
//  5. 轮询 GET /transaction 到终态
func (s *Service) DeploySafe(
	ctx context.Context,
	scopeID string,
	paymentToken common.Address,
	payment *big.Int,
	paymentReceiver common.Address,
) (string, string, error) {
	if scopeID == "" {
		return "", "", fmt.Errorf("%w: scopeID required", ErrInvalidConfig)
	}

	signerAddr := s.signer.Address()

	// 1. 预测/查 Safe 地址
	deployed, safeAddrHex, err := s.client.GetDeployed(ctx, "", signerAddr.Hex(), scopeID)
	if err != nil {
		return "", "", fmt.Errorf("get deployed: %w", err)
	}
	if deployed {
		// 已部署：等价于幂等成功，调用方不应重复 deploy（relayer-service 也会 409）。
		return "", safeAddrHex, nil
	}
	safeAddr := common.HexToAddress(safeAddrHex)

	// 2. EIP-712 digest
	scopeID32 := ParseScopeID(scopeID)
	if payment == nil {
		payment = new(big.Int)
	}
	digest := BuildSafeCreateDigest(SafeCreateInput{
		PaymentToken:    paymentToken,
		Payment:         payment,
		PaymentReceiver: paymentReceiver,
		ScopeID:         scopeID32,
	}, s.safeFactory, s.chainID)

	// 3. 签名 + v→27/28
	rawSig, err := s.signer.SignDigest(ctx, digest)
	if err != nil {
		return "", "", fmt.Errorf("sign safe-create digest: %w", err)
	}
	sig, err := normalizeSafeSig(rawSig)
	if err != nil {
		return "", "", err
	}

	// 4. /submit
	params := SafeCreateParams{
		PaymentToken:    paymentToken.Hex(),
		Payment:         payment.String(),
		PaymentReceiver: paymentReceiver.Hex(),
		ScopeId:         scopeID,
	}
	paramsJSON, _ := json.Marshal(params)
	req := &SubmitRequest{
		From:            strings.ToLower(signerAddr.Hex()),
		To:              s.safeFactory.Hex(),
		ProxyWallet:     safeAddr.Hex(),
		Signature:       "0x" + hex.EncodeToString(sig),
		SignatureParams: paramsJSON,
		Type:            TxTypeSafeCreate,
		ScopeID:         scopeID,
	}
	resp, err := s.client.Submit(ctx, req)
	if err != nil {
		return "", "", err
	}

	// 5. 轮询
	if err := s.waitTerminal(ctx, resp.TransactionID); err != nil {
		return resp.TransactionID, safeAddr.Hex(), err
	}
	return resp.TransactionID, safeAddr.Hex(), nil
}

// ExecuteSafeTx 走 SAFE 路径，返回 (transactionID, transactionHash, error)。
//
// safeAddress  — 已部署的 Safe 地址（DeploySafe 返回的，或调用方自存的）。
// to / data    — Safe 在链上要执行的目标合约 + calldata（如 USDC.approve 的
// 编码）。
// scopeID      — 必填，作为 SubmitRequest.scopeId 让 relayer 选 gas key。
//
// 流程：
//  1. GET /nonce（按 EOA + scopeId 模式）拿 Safe 当前 nonce
//  2. BuildSafeTxDigest（safeAddress + chainID 进 domain）
//  3. signer.SignDigest 签名 + v 加 27
//  4. POST /submit（type=SAFE，data 字段填 calldata 16 进制）
//  5. 轮询 GET /transaction 到终态，返回最终 hash
func (s *Service) ExecuteSafeTx(
	ctx context.Context,
	safeAddress common.Address,
	to common.Address,
	data []byte,
	scopeID string,
) (string, string, error) {
	if scopeID == "" {
		return "", "", fmt.Errorf("%w: scopeID required", ErrInvalidConfig)
	}
	if (safeAddress == common.Address{}) {
		return "", "", fmt.Errorf("%w: safeAddress required", ErrInvalidConfig)
	}

	signerAddr := s.signer.Address()

	// 1. 拉 Safe nonce（用 EOA + scopeId 模式，与 mm V2 一致）
	nonce, err := s.client.GetNonce(ctx, "", signerAddr.Hex(), scopeID)
	if err != nil {
		return "", "", fmt.Errorf("get nonce: %w", err)
	}

	// 2. EIP-712 digest（默认全零参数 = CALL + relayer 全代付）
	digest := BuildSafeTxDigest(SafeTxInput{
		To:    to,
		Data:  data,
		Nonce: new(big.Int).SetUint64(nonce),
	}, safeAddress, s.chainID)

	// 3. 签名 + v→27/28
	rawSig, err := s.signer.SignDigest(ctx, digest)
	if err != nil {
		return "", "", fmt.Errorf("sign safe-tx digest: %w", err)
	}
	sig, err := normalizeSafeSig(rawSig)
	if err != nil {
		return "", "", err
	}

	// 4. /submit
	params := SafeTxParams{
		GasPrice:       "0",
		Operation:      "0",
		SafeTxnGas:     "0",
		BaseGas:        "0",
		GasToken:       "0x0000000000000000000000000000000000000000",
		RefundReceiver: "0x0000000000000000000000000000000000000000",
	}
	paramsJSON, _ := json.Marshal(params)

	req := &SubmitRequest{
		From:            strings.ToLower(signerAddr.Hex()),
		To:              strings.ToLower(to.Hex()),
		ProxyWallet:     safeAddress.Hex(),
		Data:            "0x" + hex.EncodeToString(data),
		Nonce:           new(big.Int).SetUint64(nonce).String(),
		Signature:       "0x" + hex.EncodeToString(sig),
		SignatureParams: paramsJSON,
		Type:            TxTypeSafe,
		ScopeID:         scopeID,
	}
	resp, err := s.client.Submit(ctx, req)
	if err != nil {
		return "", "", err
	}

	// 5. 轮询
	tx, err := s.waitTerminalWithTx(ctx, resp.TransactionID)
	if err != nil {
		hashOut := ""
		if tx != nil {
			hashOut = tx.TransactionHash
		}
		return resp.TransactionID, hashOut, err
	}
	return resp.TransactionID, tx.TransactionHash, nil
}

// waitTerminal 轮询 /transaction 到终态。返回 nil 表示 STATE_CONFIRMED；
// STATE_FAILED/INVALID 返回 ErrTxFailed wrapper；超时返回 ErrTxTimeout。
func (s *Service) waitTerminal(ctx context.Context, txID string) error {
	_, err := s.waitTerminalWithTx(ctx, txID)
	return err
}

// waitTerminalWithTx 与 waitTerminal 相同，但额外返回最后一次轮询到的
// Transaction（用于拿稳定后的 transactionHash）。
func (s *Service) waitTerminalWithTx(ctx context.Context, txID string) (*Transaction, error) {
	if txID == "" {
		return nil, fmt.Errorf("%w: empty txID", ErrInvalidConfig)
	}
	var last *Transaction
	for i := 0; i < s.pollMaxAttempts; i++ {
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("%w: %v", ErrCancelled, ctx.Err())
		case <-time.After(s.pollInterval):
		}

		tx, err := s.client.GetTransaction(ctx, txID)
		if err != nil {
			// transient 错误——让循环继续重试到 timeout 边界，但 ctx 取消由上面 select 处理。
			// 永久 4xx（如 404 not found）按上游错误立即返回。
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrAuth) || errors.Is(err, ErrPrecondition) {
				return last, err
			}
			continue
		}
		last = tx
		switch tx.State {
		case StateConfirmed:
			return tx, nil
		case StateFailed, StateInvalid:
			return tx, fmt.Errorf("%w: tx %s state=%s msg=%s", ErrTxFailed, txID, tx.State, tx.ErrorMessage)
		}
	}
	return last, fmt.Errorf("%w: tx %s after %d polls", ErrTxTimeout, txID, s.pollMaxAttempts)
}
