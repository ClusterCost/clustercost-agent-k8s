package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

type fakePodMetricsLister struct {
	responses []*metricsv1beta1.PodMetricsList
	errors    []error
	call      int
}

func (f *fakePodMetricsLister) List(ctx context.Context, opts metav1.ListOptions) (*metricsv1beta1.PodMetricsList, error) {
	defer func() { f.call++ }()
	var resp *metricsv1beta1.PodMetricsList
	var err error
	if f.call < len(f.responses) {
		resp = f.responses[f.call]
	}
	if f.call < len(f.errors) {
		err = f.errors[f.call]
	}
	if resp == nil {
		resp = &metricsv1beta1.PodMetricsList{}
	}
	return resp, err
}

func TestMetricsCollectorCachesOnError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	firstResp := &metricsv1beta1.PodMetricsList{
		Items: []metricsv1beta1.PodMetrics{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-a",
					Namespace: "default",
				},
				Containers: []metricsv1beta1.ContainerMetrics{
					{
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("300m"),
							corev1.ResourceMemory: resource.MustParse("400Mi"),
						},
					},
				},
			},
		},
	}

	fakeLister := &fakePodMetricsLister{
		responses: []*metricsv1beta1.PodMetricsList{firstResp, nil},
		errors:    []error{nil, errors.New("metrics unavailable")},
	}

	collector := &MetricsCollector{
		pods:   fakeLister,
		logger: logger,
	}

	ctx := context.Background()
	first, err := collector.CollectPodMetrics(ctx)
	if err != nil {
		t.Fatalf("first CollectPodMetrics err: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("expected 1 metric result, got %d", len(first))
	}

	second, err := collector.CollectPodMetrics(ctx)
	if err == nil {
		t.Fatalf("expected error on second call")
	}
	if len(second) != 1 {
		t.Fatalf("expected cached metrics on error, got %d", len(second))
	}
}
