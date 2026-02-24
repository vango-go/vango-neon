package neon

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type healthDBStub struct {
	pingFunc func(ctx context.Context) error
}

func (d *healthDBStub) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	panic("unexpected Exec call")
}

func (d *healthDBStub) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	panic("unexpected Query call")
}

func (d *healthDBStub) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	panic("unexpected QueryRow call")
}

func (d *healthDBStub) Begin(_ context.Context) (pgx.Tx, error) {
	panic("unexpected Begin call")
}

func (d *healthDBStub) BeginTx(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	panic("unexpected BeginTx call")
}

func (d *healthDBStub) Ping(ctx context.Context) error {
	if d.pingFunc == nil {
		return nil
	}
	return d.pingFunc(ctx)
}

func (d *healthDBStub) Close() {}

func TestHealthCheck_ReturnsStatusOK(t *testing.T) {
	t.Parallel()

	status, err := HealthCheck(context.Background(), &healthDBStub{})
	if err != nil {
		t.Fatalf("HealthCheck() error = %v", err)
	}
	if status == nil {
		t.Fatal("HealthCheck() returned nil status")
	}
	if status.Status != "ok" {
		t.Fatalf("status.Status=%q, want %q", status.Status, "ok")
	}
	if status.Database != "neon" {
		t.Fatalf("status.Database=%q, want %q", status.Database, "neon")
	}
}

func TestHealthCheck_ReturnsSafeErrorOnPingFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("ping failed for postgresql://user:supersecret@db.example.com/neondb")

	_, err := HealthCheck(context.Background(), &healthDBStub{
		pingFunc: func(_ context.Context) error {
			return sentinel
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	assertSafeErrorWraps(t, err, sentinel)
	if got, want := err.Error(), "neon: health check failed"; got != want {
		t.Fatalf("error=%q, want %q", got, want)
	}
	assertNoDSNLeak(t, err.Error())
}
