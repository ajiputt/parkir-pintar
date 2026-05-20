// Package-internal (white-box) tests untuk fungsi unexported.
// Tests untuk public API ada di tracing_test.go (black-box).
package tracing

import "testing"

// stubEnv returns getEnv stub yang lookup ke map static — pakai ini biar
// tidak pollute os.Getenv di test paralel.
func stubEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveSamplerRatio_DefaultProd(t *testing.T) {
	t.Parallel()
	// Empty cfg + no env override → default 0.1 (10%).
	got := resolveSamplerRatio(Config{Env: "prod"}, stubEnv(nil))
	if got != 0.1 {
		t.Fatalf("default prod ratio = %v, want 0.1", got)
	}
}

func TestResolveSamplerRatio_DevForcesFull(t *testing.T) {
	t.Parallel()
	// Env=dev → force 1.0 (trace semuanya).
	got := resolveSamplerRatio(Config{Env: "dev", SamplerRatio: 0.1}, stubEnv(nil))
	if got != 1.0 {
		t.Fatalf("dev ratio = %v, want 1.0", got)
	}
}

func TestResolveSamplerRatio_ConfigRespected(t *testing.T) {
	t.Parallel()
	// Explicit SamplerRatio dipakai (kalau Env != dev dan tidak ada env override).
	got := resolveSamplerRatio(Config{Env: "staging", SamplerRatio: 0.25}, stubEnv(nil))
	if got != 0.25 {
		t.Fatalf("ratio = %v, want 0.25", got)
	}
}

func TestResolveSamplerRatio_NegativeFallback(t *testing.T) {
	t.Parallel()
	// Negative SamplerRatio → fall back ke default 0.1.
	got := resolveSamplerRatio(Config{Env: "prod", SamplerRatio: -0.5}, stubEnv(nil))
	if got != 0.1 {
		t.Fatalf("negative ratio fallback: got %v, want 0.1", got)
	}
}

func TestResolveSamplerRatio_EnvOverrideValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		env   string
		want  float64
		input Config
	}{
		{"override-to-full", "1.0", 1.0, Config{Env: "prod"}},
		{"override-to-zero", "0", 0, Config{Env: "prod", SamplerRatio: 0.5}},
		{"override-beats-dev", "0.5", 0.5, Config{Env: "dev"}},
		{"override-precise", "0.05", 0.05, Config{Env: "prod"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveSamplerRatio(tc.input, stubEnv(map[string]string{
				"OTEL_TRACES_SAMPLER_RATIO": tc.env,
			}))
			if got != tc.want {
				t.Fatalf("ratio = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveSamplerRatio_EnvOverrideInvalid(t *testing.T) {
	t.Parallel()
	// Invalid env values → fall back ke ratio sebelumnya (cfg/default).
	cases := []struct {
		name   string
		envVal string
	}{
		{"non-numeric", "abc"},
		{"out-of-range-high", "1.5"},
		{"out-of-range-low", "-0.1"},
		{"way-too-high", "100"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Prod default 0.1; invalid override harus tidak mengubah.
			got := resolveSamplerRatio(
				Config{Env: "prod"},
				stubEnv(map[string]string{"OTEL_TRACES_SAMPLER_RATIO": tc.envVal}),
			)
			if got != 0.1 {
				t.Fatalf("invalid env %q changed ratio: got %v, want 0.1 (default unchanged)", tc.envVal, got)
			}
		})
	}
}

func TestResolveSamplerRatio_BoundaryValues(t *testing.T) {
	t.Parallel()
	// Boundary [0, 1] inclusive — valid edges.
	cases := []struct {
		envVal string
		want   float64
	}{
		{"0", 0},
		{"1", 1},
		{"0.0", 0},
		{"1.0", 1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.envVal, func(t *testing.T) {
			t.Parallel()
			got := resolveSamplerRatio(
				Config{Env: "prod"},
				stubEnv(map[string]string{"OTEL_TRACES_SAMPLER_RATIO": tc.envVal}),
			)
			if got != tc.want {
				t.Fatalf("boundary %q: got %v, want %v", tc.envVal, got, tc.want)
			}
		})
	}
}

func TestResolveSamplerRatio_EmptyEnvIgnored(t *testing.T) {
	t.Parallel()
	// Empty env value → tidak override, pakai cfg.
	got := resolveSamplerRatio(
		Config{Env: "staging", SamplerRatio: 0.3},
		stubEnv(map[string]string{"OTEL_TRACES_SAMPLER_RATIO": ""}),
	)
	if got != 0.3 {
		t.Fatalf("empty env should not override: got %v, want 0.3", got)
	}
}
