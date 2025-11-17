package snapshot

import "testing"

func TestEnvironmentClassifier(t *testing.T) {
	classifier := NewEnvironmentClassifier(ClassifierConfig{
		LabelKeys:              []string{"clustercost.io/environment"},
		ProductionLabelValues:  []string{"production", "prod"},
		NonProdLabelValues:     []string{"nonprod", "dev"},
		SystemLabelValues:      []string{"system"},
		ProductionNameContains: []string{"prod"},
		SystemNamespaces:       []string{"kube-system", "monitoring"},
	})

	tests := []struct {
		name    string
		ns      string
		labels  map[string]string
		wantEnv string
	}{
		{
			name:    "label overrides to production",
			ns:      "foo",
			labels:  map[string]string{"clustercost.io/environment": "prod"},
			wantEnv: "production",
		},
		{
			name:    "label unknown value falls back to unknown",
			ns:      "foo",
			labels:  map[string]string{"clustercost.io/environment": "weird"},
			wantEnv: "unknown",
		},
		{
			name:    "system namespace recognized",
			ns:      "kube-system",
			wantEnv: "system",
		},
		{
			name:    "production detected from name contains",
			ns:      "payments-prod",
			wantEnv: "production",
		},
		{
			name:    "default to nonprod",
			ns:      "default",
			wantEnv: "nonprod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifier.Classify(tt.ns, tt.labels); got != tt.wantEnv {
				t.Fatalf("Classify(%q)=%q want %q", tt.ns, got, tt.wantEnv)
			}
		})
	}
}
