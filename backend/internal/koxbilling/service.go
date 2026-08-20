// Package koxbilling owns the durable Sub2API-to-Kox billing boundary.
package koxbilling

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	internalAPIKeyEnv = "KOX_BILLING_INTERNAL_API_KEY"
	webhookSecretEnv  = "SUB2API_WEBHOOK_SECRET"
	webhookURLEnv     = "SUB2API_WEBHOOK_URL"
	gatewayUserIDEnv  = "KOX_GATEWAY_USER_ID"
	gatewayGroupIDEnv = "KOX_GATEWAY_GROUP_ID"

	koxOutboxBatchSize     = 20
	koxOutboxPollInterval  = 5 * time.Second
	koxOutboxClaimTimeout  = 5 * time.Second
	koxOutboxClaimLease    = 10 * time.Minute
	koxWebhookTimeout      = 10 * time.Second
	koxOutboxUpdateTimeout = 5 * time.Second
)

type Service struct {
	db                                     *sql.DB
	internalKey, webhookSecret, webhookURL string
	gatewayUserID, gatewayGroupID           int64
	workerID                                string
	client                                  *http.Client
}
type CreateKeyInput struct {
	KoxCompanyID string `json:"kox_company_id"`
	KoxUserID    string `json:"kox_user_id"`
}
type UsageInput struct {
	APIKeyID          string         `json:"api_key_id"`
	ProviderRequestID string         `json:"provider_request_id"`
	RequestID         string         `json:"request_id"`
	ReservationID     string         `json:"reservation_id"`
	BusinessCode      string         `json:"business_code"`
	Model             string         `json:"model"`
	BillingType       string         `json:"billing_type"`
	ActualCost        string         `json:"actual_cost"`
	Currency          string         `json:"currency"`
	Status            string         `json:"status"`
	InputTokens       int            `json:"input_tokens"`
	OutputTokens      int            `json:"output_tokens"`
	CacheReadTokens   int            `json:"cache_read_tokens"`
	CacheWriteTokens  int            `json:"cache_write_tokens"`
	OccurredAt        time.Time      `json:"occurred_at"`
	Metadata          map[string]any `json:"metadata"`
}
type Key struct{ ID, AccountID, KoxUserID, Fingerprint, Status string }
type Usage struct {
	UsageLogID       string          `json:"usage_log_id"`
	APIKeyID         string          `json:"api_key_id"`
	RequestID        string          `json:"request_id"`
	ReservationID    string          `json:"reservation_id"`
	BusinessCode     string          `json:"business_code"`
	Model            string          `json:"model"`
	BillingType      string          `json:"billing_type"`
	Revision         int             `json:"revision"`
	InputTokens      int             `json:"input_tokens"`
	OutputTokens     int             `json:"output_tokens"`
	CacheReadTokens  int             `json:"cache_read_tokens"`
	CacheWriteTokens int             `json:"cache_write_tokens"`
	ActualCost       string          `json:"actual_cost"`
	Currency         string          `json:"currency"`
	Status           string          `json:"status"`
	OccurredAt       time.Time       `json:"occurred_at"`
	Metadata         json.RawMessage `json:"metadata"`
}

func New(db *sql.DB) *Service {
	userID, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv(gatewayUserIDEnv)), 10, 64)
	groupID, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv(gatewayGroupIDEnv)), 10, 64)
	return &Service{db: db, internalKey: strings.TrimSpace(os.Getenv(internalAPIKeyEnv)), webhookSecret: strings.TrimSpace(os.Getenv(webhookSecretEnv)), webhookURL: strings.TrimSpace(os.Getenv(webhookURLEnv)), gatewayUserID: userID, gatewayGroupID: groupID, workerID: uuid.NewString(), client: &http.Client{Timeout: koxWebhookTimeout}}
}

func (s *Service) Enabled() bool { return s != nil && s.db != nil && s.internalKey != "" && s.gatewayUserID > 0 && s.gatewayGroupID > 0 }
func (s *Service) Authorize(value string) bool {
	if !s.Enabled() {
		return false
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	return hmac.Equal([]byte(value), []byte(s.internalKey))
}

func (s *Service) CreateKey(ctx context.Context, in CreateKeyInput) (Key, string, error) {
	if strings.TrimSpace(in.KoxCompanyID) == "" || strings.TrimSpace(in.KoxUserID) == "" {
		return Key{}, "", errors.New("kox_company_id and kox_user_id are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Key{}, "", err
	}
	defer tx.Rollback()
	accountID := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO kox_service_accounts(account_id,kox_company_id) VALUES($1,$2) ON CONFLICT(kox_company_id) DO UPDATE SET updated_at=NOW()`, accountID, in.KoxCompanyID); err != nil {
		return Key{}, "", err
	}
	if err = tx.QueryRowContext(ctx, `SELECT account_id FROM kox_service_accounts WHERE kox_company_id=$1`, in.KoxCompanyID).Scan(&accountID); err != nil {
		return Key{}, "", err
	}
	// Creation is retried by the Kox outbox and may arrive concurrently. Keep
	// exactly one active gateway key for a company/user pair.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, in.KoxCompanyID, in.KoxUserID); err != nil {
		return Key{}, "", err
	}
	var existing Key
	var existingPlain string
	err = tx.QueryRowContext(ctx, `SELECT k.api_key_id,k.account_id,k.kox_user_id,k.key_fingerprint,k.status,g.key
		FROM kox_api_keys k JOIN api_keys g ON g.id=k.gateway_api_key_id
		WHERE k.account_id=$1 AND k.kox_user_id=$2 AND k.status='active'
		  AND g.status='active' AND g.group_id IS NOT NULL AND g.deleted_at IS NULL`, accountID, in.KoxUserID).
		Scan(&existing.ID, &existing.AccountID, &existing.KoxUserID, &existing.Fingerprint, &existing.Status, &existingPlain)
	if err == nil {
		if err = tx.Commit(); err != nil {
			return Key{}, "", err
		}
		return existing, existingPlain, nil
	}
	if err != sql.ErrNoRows {
		return Key{}, "", err
	}
	plain, err := randomKey()
	if err != nil {
		return Key{}, "", err
	}
	digest := digestKey(plain)
	id := uuid.NewString()
	fingerprint := keyFingerprint(plain)
	var gatewayKeyID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id,status) VALUES($1,$2,$3,$4,'active') RETURNING id`, s.gatewayUserID, plain, "kox:"+in.KoxCompanyID+":"+in.KoxUserID, s.gatewayGroupID).Scan(&gatewayKeyID); err != nil {
		return Key{}, "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO kox_api_keys(api_key_id,account_id,kox_user_id,key_digest,key_fingerprint,gateway_api_key_id) VALUES($1,$2,$3,$4,$5,$6)`, id, accountID, in.KoxUserID, digest, fingerprint, gatewayKeyID)
	if err != nil {
		return Key{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return Key{}, "", err
	}
	return Key{ID: id, AccountID: accountID, KoxUserID: in.KoxUserID, Fingerprint: fingerprint, Status: "active"}, plain, nil
}

func (s *Service) ListKeys(ctx context.Context, accountID, userID string) ([]Key, error) {
	q := `SELECT api_key_id,account_id,kox_user_id,key_fingerprint,status FROM kox_api_keys WHERE 1=1`
	args := []any{}
	if accountID != "" {
		args = append(args, accountID)
		q += fmt.Sprintf(" AND account_id=$%d", len(args))
	}
	if userID != "" {
		args = append(args, userID)
		q += fmt.Sprintf(" AND kox_user_id=$%d", len(args))
	}
	q += " ORDER BY created_at DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.ID, &k.AccountID, &k.KoxUserID, &k.Fingerprint, &k.Status); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s *Service) Credential(ctx context.Context, id string) (Key, string, error) {
	var key Key
	var plain string
	err := s.db.QueryRowContext(ctx, `SELECT k.api_key_id,k.account_id,k.kox_user_id,k.key_fingerprint,k.status,g.key
		FROM kox_api_keys k JOIN api_keys g ON g.id=k.gateway_api_key_id
		WHERE k.api_key_id=$1 AND k.status='active' AND g.status='active' AND g.group_id IS NOT NULL AND g.deleted_at IS NULL`, id).
		Scan(&key.ID, &key.AccountID, &key.KoxUserID, &key.Fingerprint, &key.Status, &plain)
	if err != nil {
		return Key{}, "", err
	}
	return key, plain, nil
}
func (s *Service) Disable(ctx context.Context, id string) error {
	return s.setKeyStatus(ctx, id, "disabled")
}
func (s *Service) Rotate(ctx context.Context, id string) (Key, string, error) {
	var company, user string
	err := s.db.QueryRowContext(ctx, `SELECT a.kox_company_id,k.kox_user_id FROM kox_api_keys k JOIN kox_service_accounts a ON a.account_id=k.account_id WHERE k.api_key_id=$1`, id).Scan(&company, &user)
	if err != nil {
		return Key{}, "", err
	}
	if err = s.setKeyStatus(ctx, id, "rotated"); err != nil {
		return Key{}, "", err
	}
	return s.CreateKey(ctx, CreateKeyInput{company, user})
}
func (s *Service) setKeyStatus(ctx context.Context, id, status string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var gatewayKeyID sql.NullInt64
	if e = tx.QueryRowContext(ctx, `SELECT gateway_api_key_id FROM kox_api_keys WHERE api_key_id=$1 AND status='active'`, id).Scan(&gatewayKeyID); e != nil {
		return e
	}
	r, e := tx.ExecContext(ctx, `UPDATE kox_api_keys SET status=$2,disabled_at=NOW() WHERE api_key_id=$1 AND status='active'`, id, status)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return sql.ErrNoRows
	}
	if gatewayKeyID.Valid {
		if _, e = tx.ExecContext(ctx, `UPDATE api_keys SET status='disabled',updated_at=NOW() WHERE id=$1`, gatewayKeyID.Int64); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// RecordUsage is the only write path for Kox usage.  The event payload is
// marshalled once and those exact bytes are used by the delivery worker.
func (s *Service) RecordUsage(ctx context.Context, in UsageInput) (string, error) {
	if err := validateUsage(in); err != nil {
		return "", err
	}
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}
	if in.Metadata == nil {
		in.Metadata = map[string]any{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT TRUE FROM kox_api_keys WHERE api_key_id=$1 AND status='active'`, in.APIKeyID).Scan(&active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("api key is not active")
		}
		return "", err
	}
	var usageID string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT usage_log_id,revision FROM kox_usage_logs WHERE provider_request_id=$1 FOR UPDATE`, in.ProviderRequestID).Scan(&usageID, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		usageID = uuid.NewString()
		revision = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO kox_usage_logs(usage_log_id,api_key_id,provider_request_id,request_id,reservation_id,business_code,model,billing_type,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,actual_cost,currency,status,occurred_at,revision,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, usageID, in.APIKeyID, in.ProviderRequestID, in.RequestID, in.ReservationID, in.BusinessCode, in.Model, in.BillingType, in.InputTokens, in.OutputTokens, in.CacheReadTokens, in.CacheWriteTokens, in.ActualCost, in.Currency, in.Status, in.OccurredAt, revision, in.Metadata)
	} else if err == nil {
		revision++
		_, err = tx.ExecContext(ctx, `UPDATE kox_usage_logs SET request_id=$2,reservation_id=$3,business_code=$4,model=$5,billing_type=$6,input_tokens=$7,output_tokens=$8,cache_read_tokens=$9,cache_write_tokens=$10,actual_cost=$11,currency=$12,status=$13,occurred_at=$14,revision=$15,metadata=$16,updated_at=NOW() WHERE usage_log_id=$1`, usageID, in.RequestID, in.ReservationID, in.BusinessCode, in.Model, in.BillingType, in.InputTokens, in.OutputTokens, in.CacheReadTokens, in.CacheWriteTokens, in.ActualCost, in.Currency, in.Status, in.OccurredAt, revision, in.Metadata)
	}
	if err != nil {
		return "", err
	}
	payload := map[string]any{"event_id": uuid.NewString(), "usage_log_id": usageID, "revision": revision, "reservation_id": in.ReservationID, "external_call_id": in.RequestID, "api_key_id": in.APIKeyID, "business_code": in.BusinessCode, "model": in.Model, "billing_type": in.BillingType, "input_tokens": in.InputTokens, "output_tokens": in.OutputTokens, "cache_read_tokens": in.CacheReadTokens, "cache_write_tokens": in.CacheWriteTokens, "actual_cost": in.ActualCost, "currency": in.Currency, "status": in.Status, "occurred_at": in.OccurredAt.UTC().Format(time.RFC3339Nano), "metadata": in.Metadata}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	eventID := payload["event_id"].(string)
	_, err = tx.ExecContext(ctx, `INSERT INTO kox_billing_outbox(event_id,usage_log_id,revision,payload) VALUES($1,$2,$3,$4)`, eventID, usageID, revision, raw)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return usageID, nil
}

func (s *Service) DeliverDue(ctx context.Context, limit int) error {
	if s.webhookURL == "" || s.webhookSecret == "" {
		return nil
	}
	if limit < 1 || limit > koxOutboxBatchSize {
		limit = koxOutboxBatchSize
	}
	claimCtx, cancel := context.WithTimeout(ctx, koxOutboxClaimTimeout)
	defer cancel()
	rows, err := s.db.QueryContext(claimCtx, `
		WITH candidates AS MATERIALIZED (
			SELECT event_id
			FROM kox_billing_outbox
			WHERE delivery_status = 'pending'
			  AND next_attempt_at <= NOW()
			  AND (claimed_at IS NULL OR claimed_at < NOW() - ($3 * INTERVAL '1 second'))
			ORDER BY created_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE kox_billing_outbox AS o
			SET claimed_at = NOW(), claimed_by = $1
			FROM candidates AS c
			WHERE o.event_id = c.event_id
			RETURNING o.event_id, o.payload, o.attempts
		)
		SELECT event_id, payload, attempts FROM claimed`, s.workerID, limit, int(koxOutboxClaimLease.Seconds()))
	if err != nil {
		return err
	}
	type claimedEvent struct {
		id       string
		payload  []byte
		attempts int
	}
	events := make([]claimedEvent, 0, limit)
	for rows.Next() {
		var event claimedEvent
		if err := rows.Scan(&event.id, &event.payload, &event.attempts); err != nil {
			_ = rows.Close()
			return err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	var deliveryErrs []error
	for _, event := range events {
		if err := s.deliver(ctx, event.id, event.payload, event.attempts); err != nil {
			deliveryErrs = append(deliveryErrs, fmt.Errorf("deliver outbox event %s: %w", event.id, err))
		}
	}
	return errors.Join(deliveryErrs...)
}

func (s *Service) deliver(ctx context.Context, id string, raw []byte, attempts int) error {
	deliveryCtx, cancel := context.WithTimeout(ctx, koxWebhookTimeout)
	defer cancel()
	now := time.Now().UTC()
	timestamp := fmt.Sprint(now.Unix())
	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(raw)
	req, err := http.NewRequestWithContext(deliveryCtx, http.MethodPost, s.webhookURL, bytes.NewReader(raw))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Sub2API-Timestamp", timestamp)
		req.Header.Set("X-Sub2API-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		var resp *http.Response
		resp, err = s.client.Do(req)
		if resp != nil {
			body, readErr := readWebhookResponse(resp.Body)
			if err == nil && readErr != nil {
				err = fmt.Errorf("read webhook response: %w", readErr)
			}
			if err == nil {
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					return s.updateDelivery(ctx, `UPDATE kox_billing_outbox SET delivery_status='delivered',attempts=attempts+1,first_attempted_at=COALESCE(first_attempted_at,NOW()),last_attempted_at=NOW(),completed_at=NOW(),last_response=$2,claimed_at=NULL,claimed_by=NULL WHERE event_id=$1 AND claimed_by=$3`, id, body, s.workerID)
				}
				if resp.StatusCode == 401 {
					return s.updateDelivery(ctx, `UPDATE kox_billing_outbox SET delivery_status='blocked',last_error=$2,last_response=$3,last_attempted_at=NOW(),claimed_at=NULL,claimed_by=NULL WHERE event_id=$1 AND claimed_by=$4`, id, "webhook returned 401", body, s.workerID)
				}
				if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
					return s.updateDelivery(ctx, `UPDATE kox_billing_outbox SET delivery_status='dead_letter',last_error=$2,last_response=$3,last_attempted_at=NOW(),claimed_at=NULL,claimed_by=NULL WHERE event_id=$1 AND claimed_by=$4`, id, fmt.Sprintf("webhook returned %d", resp.StatusCode), body, s.workerID)
				}
				err = fmt.Errorf("webhook returned %d", resp.StatusCode)
			}
		}
	}
	delay := time.Duration(1<<min(attempts, 8)) * time.Second
	return s.updateDelivery(ctx, `UPDATE kox_billing_outbox SET attempts=attempts+1,first_attempted_at=COALESCE(first_attempted_at,NOW()),last_attempted_at=NOW(),next_attempt_at=NOW()+($2 * INTERVAL '1 second'),last_error=$3,claimed_at=NULL,claimed_by=NULL WHERE event_id=$1 AND claimed_by=$4`, id, int(delay.Seconds()), errorText(err), s.workerID)
}

func readWebhookResponse(body io.ReadCloser) (string, error) {
	defer body.Close()
	response, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return "", err
	}
	_, err = io.Copy(io.Discard, body)
	return string(response), err
}

func (s *Service) updateDelivery(ctx context.Context, query string, args ...any) error {
	updateCtx, cancel := context.WithTimeout(ctx, koxOutboxUpdateTimeout)
	defer cancel()
	if _, err := s.db.ExecContext(updateCtx, query, args...); err != nil {
		return fmt.Errorf("persist delivery state: %w", err)
	}
	return nil
}
func (s *Service) Replay(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE kox_billing_outbox SET delivery_status='pending',next_attempt_at=NOW(),last_error=NULL,claimed_at=NULL,claimed_by=NULL WHERE event_id=$1 AND delivery_status IN ('dead_letter','blocked')`, id)
	return err
}
func (s *Service) Usage(ctx context.Context, requestID, apiKeyID string, from, to time.Time) ([]Usage, error) {
	q := `SELECT usage_log_id,api_key_id,request_id,reservation_id,business_code,model,billing_type,revision,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,actual_cost::text,currency,status,occurred_at,metadata FROM kox_usage_logs WHERE 1=1`
	args := []any{}
	if requestID != "" {
		args = append(args, requestID)
		q += fmt.Sprintf(" AND request_id=$%d", len(args))
	}
	if apiKeyID != "" {
		args = append(args, apiKeyID)
		q += fmt.Sprintf(" AND api_key_id=$%d", len(args))
	}
	if !from.IsZero() {
		args = append(args, from)
		q += fmt.Sprintf(" AND occurred_at >= $%d", len(args))
	}
	if !to.IsZero() {
		args = append(args, to)
		q += fmt.Sprintf(" AND occurred_at <= $%d", len(args))
	}
	q += " ORDER BY occurred_at ASC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Usage{}
	for rows.Next() {
		var u Usage
		if err := rows.Scan(&u.UsageLogID, &u.APIKeyID, &u.RequestID, &u.ReservationID, &u.BusinessCode, &u.Model, &u.BillingType, &u.Revision, &u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens, &u.ActualCost, &u.Currency, &u.Status, &u.OccurredAt, &u.Metadata); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Service) Start(ctx context.Context) {
	if !s.Enabled() || s.webhookURL == "" || s.webhookSecret == "" {
		return
	}
	go func() {
		t := time.NewTicker(koxOutboxPollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.DeliverDue(ctx, koxOutboxBatchSize); err != nil && ctx.Err() == nil {
					slog.Error("kox billing outbox delivery failed", "error", err)
				}
			}
		}
	}()
}
func randomKey() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return "kox_" + hex.EncodeToString(b), nil
}
func digestKey(k string) string { h := sha256.Sum256([]byte(k)); return hex.EncodeToString(h[:]) }
func keyFingerprint(k string) string {
	h := sha256.Sum256([]byte(k))
	return "kox_" + hex.EncodeToString(h[:6])
}
func errorText(e error) string {
	if e == nil {
		return "delivery failed"
	}
	return e.Error()
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func validateUsage(in UsageInput) error {
	for _, v := range []string{in.APIKeyID, in.ProviderRequestID, in.RequestID, in.ReservationID, in.BusinessCode, in.Model, in.ActualCost, in.Currency, in.Status} {
		if strings.TrimSpace(v) == "" {
			return errors.New("missing required usage field")
		}
	}
	if len(in.Currency) != 3 {
		return errors.New("currency must be ISO-4217")
	}
	return nil
}
