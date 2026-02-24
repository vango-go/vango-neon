package neon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
)

func ExampleHealthCheck() {
	status, err := HealthCheck(context.Background(), &TestDB{})
	if err != nil {
		fmt.Println("unexpected error")
		return
	}
	fmt.Println(status.Status, status.Database)
	// Output: ok neon
}

func ExampleWithTx() {
	tx := &exampleTx{}
	db := &TestDB{
		BeginTxFunc: func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
			return tx, nil
		},
	}

	err := WithTx(context.Background(), db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "UPDATE projects SET name = $1 WHERE id = $2", "Demo", 1)
		return err
	})
	if err != nil {
		fmt.Println("unexpected error")
		return
	}

	fmt.Println(tx.committed, tx.rolledBack)
	// Output: true false
}

func ExampleTestDB() {
	db := &TestDB{
		QueryRowFunc: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return NewRow(42, "My Project")
		},
	}

	var id int
	var name string
	err := db.QueryRow(context.Background(), "SELECT id, name FROM projects WHERE id = $1", 42).Scan(&id, &name)
	if err != nil {
		fmt.Println("unexpected error")
		return
	}

	fmt.Println(id, name)
	// Output: 42 My Project
}

func ExampleWithPgxConfig_tracing() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	opt := WithPgxConfig(func(c *pgxpool.Config) {
		c.ConnConfig.Tracer = &tracelog.TraceLog{
			Logger: tracelog.LoggerFunc(func(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
				safe := make(map[string]any, len(data))
				for k, v := range data {
					if k == "sql" || k == "args" {
						continue
					}
					safe[k] = v
				}
				logger.InfoContext(ctx, msg, "pgx_level", level.String(), "pgx", safe)
			}),
			LogLevel: tracelog.LogLevelInfo,
		}
	})

	_ = opt
	fmt.Println("tracing configured")
	// Output: tracing configured
}

type exampleTx struct {
	committed  bool
	rolledBack bool
}

func (t *exampleTx) Begin(ctx context.Context) (pgx.Tx, error) {
	return nil, errors.New("exampleTx: nested transactions not supported")
}

func (t *exampleTx) Commit(ctx context.Context) error {
	t.committed = true
	return nil
}

func (t *exampleTx) Rollback(ctx context.Context) error {
	t.rolledBack = true
	return nil
}

func (t *exampleTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (t *exampleTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return nil
}

func (t *exampleTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (t *exampleTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (t *exampleTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (t *exampleTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return NewRows([]string{"ok"}).AddRow(true).Build(), nil
}

func (t *exampleTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return NewRow(true)
}

func (t *exampleTx) Conn() *pgx.Conn {
	return nil
}
