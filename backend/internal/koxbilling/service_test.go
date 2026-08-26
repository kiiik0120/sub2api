package koxbilling

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestRecordGatewayUsageTxWritesKoxUsageAndOutboxForMappedKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT api_key_id FROM kox_api_keys WHERE gateway_api_key_id=\\$1").
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id"}).AddRow("kox-key"))
	mock.ExpectQuery("SELECT usage_log_id,revision FROM kox_usage_logs WHERE provider_request_id=\\$1 FOR UPDATE").
		WithArgs("req-1").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO kox_usage_logs").
		WithArgs(sqlmock.AnyArg(), "kox-key", "req-1", "req-1", "sub2api:req-1", "gateway.usage", "gpt-5", "token", 10, 20, 3, 4, 0.25, "USD", "succeeded", sqlmock.AnyArg(), 1, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO kox_billing_outbox").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 1, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	s := &Service{db: db}
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	created, err := s.RecordGatewayUsageTx(context.Background(), tx, 42, GatewayUsageInput{
		ProviderRequestID: "req-1", RequestID: "req-1", ReservationID: "sub2api:req-1",
		BusinessCode: "gateway.usage", Model: "gpt-5", BillingType: "token", ActualCost: 0.25,
		Currency: "USD", Status: "succeeded", InputTokens: 10, OutputTokens: 20,
		CacheReadTokens: 3, CacheWriteTokens: 4,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordGatewayUsageTxIgnoresNonKoxKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT api_key_id FROM kox_api_keys WHERE gateway_api_key_id=\\$1").
		WithArgs(int64(42)).WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	s := &Service{db: db}
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	created, err := s.RecordGatewayUsageTx(context.Background(), tx, 42, GatewayUsageInput{ActualCost: 1})
	require.NoError(t, err)
	require.False(t, created)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeliverDueClosesClaimRowsBeforeWebhook(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	mock.ExpectQuery("(?s)WITH candidates AS MATERIALIZED.*FOR UPDATE SKIP LOCKED.*UPDATE kox_billing_outbox").
		WithArgs("worker-a", koxOutboxBatchSize, int(koxOutboxClaimLease.Seconds())).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "payload", "attempts"}).
			AddRow("event-a", []byte(`{"event_id":"event-a"}`), 0)).
		RowsWillBeClosed()
	mock.ExpectExec("UPDATE kox_billing_outbox SET delivery_status='delivered'").
		WithArgs("event-a", "", "worker-a").
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := &Service{db: db, webhookURL: server.URL, webhookSecret: "secret", workerID: "worker-a", client: server.Client()}
	require.NoError(t, s.DeliverDue(context.Background(), 0))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeliverDueFailureReleasesClaimForRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	mock.ExpectQuery("(?s)WITH candidates AS MATERIALIZED.*FOR UPDATE SKIP LOCKED.*UPDATE kox_billing_outbox").
		WithArgs("worker-a", 1, int(koxOutboxClaimLease.Seconds())).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "payload", "attempts"}).
			AddRow("event-a", []byte(`{"event_id":"event-a"}`), 2)).
		RowsWillBeClosed()
	mock.ExpectExec("UPDATE kox_billing_outbox SET attempts=attempts\\+1").
		WithArgs("event-a", 4, "webhook returned 503", "worker-a").
		WillReturnResult(sqlmock.NewResult(0, 1))

	s := &Service{db: db, webhookURL: server.URL, webhookSecret: "secret", workerID: "worker-a", client: server.Client()}
	require.NoError(t, s.DeliverDue(context.Background(), 1))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeliverDueReturnsStatePersistenceFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	mock.ExpectQuery("(?s)WITH candidates AS MATERIALIZED.*FOR UPDATE SKIP LOCKED.*UPDATE kox_billing_outbox").
		WithArgs("worker-a", 1, int(koxOutboxClaimLease.Seconds())).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "payload", "attempts"}).
			AddRow("event-a", []byte(`{"event_id":"event-a"}`), 0)).
		RowsWillBeClosed()
	mock.ExpectExec("UPDATE kox_billing_outbox SET delivery_status='delivered'").
		WithArgs("event-a", "", "worker-a").
		WillReturnError(errors.New("connection pool exhausted"))

	s := &Service{db: db, webhookURL: server.URL, webhookSecret: "secret", workerID: "worker-a", client: server.Client()}
	err = s.DeliverDue(context.Background(), 1)
	require.ErrorContains(t, err, "persist delivery state: connection pool exhausted")
	require.NoError(t, mock.ExpectationsWereMet())
}
