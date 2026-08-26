// backfill-kox-usage creates durable Kox usage/outbox rows for historical
// local usage logs that were charged before the gateway callback was wired.
// It is safe to run repeatedly: provider_request_id is derived from the local
// usage log ID and the Kox writer is revision/idempotency aware.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/Wei-Shaw/sub2api/internal/koxbilling"
	_ "github.com/lib/pq"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "PostgreSQL DSN")
	limit := flag.Int("limit", 1000, "maximum usage logs to inspect")
	dryRun := flag.Bool("dry-run", false, "only report eligible rows")
	flag.Parse()
	if *dsn == "" || *limit < 1 {
		fmt.Fprintln(os.Stderr, "usage: backfill-kox-usage -dsn <postgres-dsn> [-limit 1000] [-dry-run]")
		os.Exit(2)
	}

	db, err := sql.Open("postgres", *dsn)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		fail(err)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT ul.id, ul.api_key_id, COALESCE(ul.request_id, ''), ul.model, ul.billing_type,
		       ul.input_tokens, ul.output_tokens, ul.cache_read_tokens,
		       ul.cache_creation_tokens, ul.actual_cost, ul.total_cost,
		       ul.rate_multiplier, COALESCE(ul.account_rate_multiplier, 1),
		       COALESCE(ul.requested_model, ''), COALESCE(ul.upstream_model, ''),
		       COALESCE(ul.group_id, 0), COALESCE(ul.subscription_id, 0)
		FROM usage_logs ul
		JOIN kox_api_keys kak ON kak.gateway_api_key_id = ul.api_key_id
		WHERE ul.actual_cost > 0
		  AND NOT EXISTS (
		      SELECT 1 FROM kox_usage_logs kul
		      WHERE kul.provider_request_id = 'sub2api-repair:' || ul.id::text
		  )
		ORDER BY ul.id
		LIMIT $1`, *limit)
	if err != nil {
		fail(err)
	}
	defer rows.Close()

	svc := koxbilling.New(db)
	count := 0
	for rows.Next() {
		var (
			id, apiKeyID, input, output, cacheRead, cacheWrite, billingType int64
			requestID, model, requestedModel, upstreamModel                 string
			actualCost, totalCost, rateMultiplier, accountRateMultiplier    float64
			groupID, subscriptionID                                         int64
		)
		if err := rows.Scan(&id, &apiKeyID, &requestID, &model, &billingType,
			&input, &output, &cacheRead, &cacheWrite, &actualCost, &totalCost,
			&rateMultiplier, &accountRateMultiplier, &requestedModel, &upstreamModel,
			&groupID, &subscriptionID); err != nil {
			fail(err)
		}
		count++
		if *dryRun {
			fmt.Printf("eligible usage_log id=%d api_key_id=%d actual_cost=%.8f\n", id, apiKeyID, actualCost)
			continue
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			fail(err)
		}
		_, err = svc.RecordGatewayUsageTx(ctx, tx, apiKeyID, koxbilling.GatewayUsageInput{
			ProviderRequestID: fmt.Sprintf("sub2api-repair:%d", id),
			RequestID:         requestID,
			ReservationID:     fmt.Sprintf("sub2api:repair:%d", id),
			BusinessCode:      "gateway.usage.repair",
			Model:             model,
			BillingType:       backfillBillingTypeName(billingType),
			ActualCost:        actualCost,
			Currency:          "USD",
			Status:            "succeeded",
			InputTokens:       int(input),
			OutputTokens:      int(output),
			CacheReadTokens:   int(cacheRead),
			CacheWriteTokens:  int(cacheWrite),
			Metadata: map[string]any{
				"source_usage_log_id":     id,
				"total_cost":              totalCost,
				"rate_multiplier":         rateMultiplier,
				"account_rate_multiplier": accountRateMultiplier,
				"requested_model":         requestedModel,
				"upstream_model":          upstreamModel,
				"group_id":                groupID,
				"subscription_id":         subscriptionID,
			},
		})
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			fail(err)
		}
	}
	if err := rows.Err(); err != nil {
		fail(err)
	}
	fmt.Printf("backfilled %d Kox usage logs\n", count)
}

func backfillBillingTypeName(v int64) string {
	if v == 1 {
		return "subscription"
	}
	return "balance"
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "backfill-kox-usage:", err)
	os.Exit(1)
}
