package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestBulkUpdateEnsuresCodexFingerprintSeedWithPerRowSQL(t *testing.T) {
	exec := &recordingSQLExecutor{result: rowsAffectedResult(0)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)

	_, err := repo.BulkUpdate(context.Background(), []int64{27, 28}, service.AccountBulkUpdate{
		Extra: map[string]any{
			"codex_fingerprint_mode": "session",
			"codex_fingerprint_seed": "22222222-2222-4222-8222-222222222222",
		},
		EnsureCodexFingerprintSeed: true,
	})

	require.NoError(t, err)
	require.NotEmpty(t, exec.execQueries)
	query := normalizeSQLWhitespace(exec.execQueries[0])
	require.Contains(t, query, "jsonb_set")
	require.Contains(t, query, "gen_random_uuid()::text")
	require.Contains(t, query, "platform = 'openai' AND type = 'oauth'")
	require.Contains(t, query, "to_jsonb(extra ->> 'codex_fingerprint_seed')")
	require.Contains(t, query, codexFingerprintSeedCanonicalPattern)
	require.NotContains(t, query, "22222222-2222-4222-8222-222222222222")
	require.NotEmpty(t, exec.execArgs)
	payload, ok := exec.execArgs[0][0].([]byte)
	require.True(t, ok)
	require.Equal(t, `{"codex_fingerprint_mode":"session"}`, string(payload))
}

func TestUpdateExtraEnsuresCodexFingerprintSeedAtomicallyWhenEnabling(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET extra = .*jsonb_set.*gen_random_uuid\(\)::text.*WHERE id = \$2 AND deleted_at IS NULL`).
		WithArgs(`{"codex_fingerprint_mode":"device"}`, int64(27)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(27), nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	repo := newAccountRepositoryWithSQL(client, db, nil)

	err = repo.UpdateExtra(context.Background(), 27, map[string]any{
		"codex_fingerprint_mode": "device",
		"codex_fingerprint_seed": "22222222-2222-4222-8222-222222222222",
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureCodexFingerprintSeedReturnsAtomicallyCreatedSeed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectQuery(`(?s)UPDATE accounts SET extra = .*gen_random_uuid\(\)::text.*WHERE id = \$1.*NOT COALESCE\(.*false\).*RETURNING extra ->> 'codex_fingerprint_seed'`).
		WithArgs(int64(27)).
		WillReturnRows(sqlmock.NewRows([]string{"seed"}).AddRow("33333333-3333-4333-8333-333333333333"))

	seed, err := repo.EnsureCodexFingerprintSeed(context.Background(), 27)

	require.NoError(t, err)
	require.Equal(t, "33333333-3333-4333-8333-333333333333", seed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureAccountExtraValueReturnsExistingFirstWriterWinner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectQuery(`(?s)UPDATE accounts SET extra = jsonb_set.*AND NOT .* \? \$2.*RETURNING extra -> \$2`).
		WithArgs(int64(27), "codex_ua_persona", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"value"}))
	mock.ExpectQuery(`(?s)SELECT extra -> \$2 FROM accounts WHERE id = \$1`).
		WithArgs(int64(27), "codex_ua_persona").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow([]byte(`{"platform":"windows","sandbox":"none"}`)))

	winner, err := repo.EnsureAccountExtraValue(context.Background(), 27, "codex_ua_persona", map[string]any{
		"platform": "mac",
		"sandbox":  "seatbelt",
	})

	require.NoError(t, err)
	require.Equal(t, map[string]any{"platform": "windows", "sandbox": "none"}, winner)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompareAndSwapAccountExtraValueReturnsConcurrentWinner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectQuery(`(?s)UPDATE accounts SET extra = jsonb_set.*COALESCE\(extra -> \$2, 'null'::jsonb\) = \$3::jsonb.*RETURNING extra -> \$2`).
		WithArgs(int64(27), "codex_device_pool", "null", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"value"}))
	mock.ExpectQuery(`(?s)SELECT extra -> \$2 FROM accounts WHERE id = \$1`).
		WithArgs(int64(27), "codex_device_pool").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow([]byte(`{"version":1,"next_slot":2,"slots":[{"id":1,"platform":"mac","sandbox":"seatbelt","created_for":"101"}]}`)))

	winner, swapped, err := repo.CompareAndSwapAccountExtraValue(
		context.Background(), 27, "codex_device_pool", nil,
		map[string]any{"version": 1, "next_slot": 2},
	)

	require.NoError(t, err)
	require.False(t, swapped)
	require.Equal(t, map[string]any{
		"version": float64(1), "next_slot": float64(2),
		"slots": []any{map[string]any{
			"id": float64(1), "platform": "mac", "sandbox": "seatbelt", "created_for": "101",
		}},
	}, winner)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateCodexFingerprintSeedRollsBackWhenUpdateFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET extra = .*gen_random_uuid\(\)::text.*WHERE id = ANY\(\$2\)`).
		WithArgs(sqlmock.AnyArg(), `{27,28}`).
		WillReturnError(errors.New("update failed"))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, db, nil)
	rows, err := repo.BulkUpdate(context.Background(), []int64{27, 28}, service.AccountBulkUpdate{
		Extra: map[string]any{
			"codex_fingerprint_mode": "session",
		},
		EnsureCodexFingerprintSeed: true,
	})

	require.EqualError(t, err, "update failed")
	require.Zero(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateCodexFingerprintSeedRollsBackWhenOutboxFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET extra = .*gen_random_uuid\(\)::text.*WHERE id = ANY\(\$2\)`).
		WithArgs(sqlmock.AnyArg(), `{27,28}`).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WillReturnError(errors.New("outbox failed"))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(client, db, nil)
	rows, err := repo.BulkUpdate(context.Background(), []int64{27, 28}, service.AccountBulkUpdate{
		Extra: map[string]any{
			"codex_fingerprint_mode": "full",
		},
		EnsureCodexFingerprintSeed: true,
	})

	require.EqualError(t, err, "outbox failed")
	require.Zero(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}
