package koxbilling

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

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
