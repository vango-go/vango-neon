package neon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConnect_HealthChecksDisabled_UsesPositiveDisabledInterval(t *testing.T) {
	cause := errors.New("constructor failed")
	called := false
	var got time.Duration

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		called = true
		got = cfg.HealthCheckPeriod
		return nil, cause
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	_, err := Connect(context.Background(), Config{
		ConnectionString:     "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
		HealthChecksDisabled: true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !called {
		t.Fatal("expected newPoolWithConfig to be called")
	}
	if got <= 0 {
		t.Fatalf("HealthCheckPeriod=%v, want > 0", got)
	}
	if got != disabledHealthCheckPeriod {
		t.Fatalf("HealthCheckPeriod=%v, want %v", got, disabledHealthCheckPeriod)
	}
}

func TestConnect_HealthChecksDisabled_IgnoresHealthCheckPeriod(t *testing.T) {
	cause := errors.New("constructor failed")
	called := false
	var got time.Duration

	original := newPoolWithConfig
	newPoolWithConfig = func(_ context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		called = true
		got = cfg.HealthCheckPeriod
		return nil, cause
	}
	t.Cleanup(func() {
		newPoolWithConfig = original
	})

	_, err := Connect(context.Background(), Config{
		ConnectionString:     "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
		HealthChecksDisabled: true,
		HealthCheckPeriod:    5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !called {
		t.Fatal("expected newPoolWithConfig to be called")
	}
	if got != disabledHealthCheckPeriod {
		t.Fatalf("HealthCheckPeriod=%v, want %v", got, disabledHealthCheckPeriod)
	}
}

func TestConnect_HealthCheckPeriod_DefaultAndCustomWhenEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want time.Duration
	}{
		{
			name: "default-when-zero",
			cfg: Config{
				ConnectionString: "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
			},
			want: defaultHealthCheckPeriod,
		},
		{
			name: "custom-when-positive",
			cfg: Config{
				ConnectionString:  "postgresql://user:pass@ep-demo.us-east-2.aws.neon.tech/neondb?sslmode=require",
				HealthCheckPeriod: 7 * time.Second,
			},
			want: 7 * time.Second,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New("constructor failed")
			called := false
			var got time.Duration

			original := newPoolWithConfig
			newPoolWithConfig = func(_ context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
				called = true
				got = cfg.HealthCheckPeriod
				return nil, cause
			}
			t.Cleanup(func() {
				newPoolWithConfig = original
			})

			_, err := Connect(context.Background(), tc.cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !called {
				t.Fatal("expected newPoolWithConfig to be called")
			}
			if got != tc.want {
				t.Fatalf("HealthCheckPeriod=%v, want %v", got, tc.want)
			}
		})
	}
}
