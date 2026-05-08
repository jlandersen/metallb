// SPDX-License-Identifier:Apache-2.0

package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.universe.tf/metallb/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func node(name string, labels map[string]string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
	}
}

func TestPreferredNodeScores(t *testing.T) {
	nodes := []corev1.Node{
		node("edge-a", map[string]string{"role": "edge", "zone": "primary"}),
		node("edge-b", map[string]string{"role": "edge", "zone": "secondary"}),
		node("worker-a", map[string]string{"role": "worker", "zone": "primary"}),
		node("worker-b", map[string]string{"role": "worker"}),
	}

	tests := []struct {
		desc      string
		nodes     []corev1.Node
		eligible  map[string]bool
		preferred []v1beta1.PreferredNodeSelector
		want      map[string]int64
		wantErr   bool
	}{
		{
			desc:     "no preferences returns nil",
			eligible: map[string]bool{"edge-a": true, "worker-a": true},
		},
		{
			desc:     "single selector scores matching eligible nodes",
			eligible: map[string]bool{"edge-a": true, "edge-b": true, "worker-a": true, "worker-b": true},
			preferred: []v1beta1.PreferredNodeSelector{
				{Weight: 100, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "edge"}}},
			},
			want: map[string]int64{"edge-a": 100, "edge-b": 100},
		},
		{
			desc:     "cumulative weights sum",
			eligible: map[string]bool{"edge-a": true, "edge-b": true, "worker-a": true, "worker-b": true},
			preferred: []v1beta1.PreferredNodeSelector{
				{Weight: 60, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"zone": "primary"}}},
				{Weight: 50, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "edge"}}},
			},
			want: map[string]int64{"edge-a": 110, "edge-b": 50, "worker-a": 60},
		},
		{
			desc:     "preferences do not score ineligible nodes",
			eligible: map[string]bool{"edge-a": true, "edge-b": true},
			preferred: []v1beta1.PreferredNodeSelector{
				{Weight: 100, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"zone": "primary"}}},
			},
			want: map[string]int64{"edge-a": 100},
		},
		{
			desc:     "empty preference matches every eligible node",
			eligible: map[string]bool{"edge-a": true, "worker-b": true},
			preferred: []v1beta1.PreferredNodeSelector{
				{Weight: 10, Preference: metav1.LabelSelector{}},
			},
			want: map[string]int64{"edge-a": 10, "worker-b": 10},
		},
		{
			desc:     "no matches returns nil",
			eligible: map[string]bool{"edge-a": true, "edge-b": true},
			preferred: []v1beta1.PreferredNodeSelector{
				{Weight: 50, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "gpu"}}},
			},
		},
		{
			desc:     "equal weights apply independently per selector",
			nodes:    []corev1.Node{node("edge-a", map[string]string{"role": "edge"}), node("worker-a", map[string]string{"role": "worker"})},
			eligible: map[string]bool{"edge-a": true, "worker-a": true},
			preferred: []v1beta1.PreferredNodeSelector{
				{Weight: 50, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "edge"}}},
				{Weight: 50, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "worker"}}},
			},
			want: map[string]int64{"edge-a": 50, "worker-a": 50},
		},
		{
			desc:     "invalid label selector operator",
			nodes:    []corev1.Node{node("n", map[string]string{"a": "b"})},
			eligible: map[string]bool{"n": true},
			preferred: []v1beta1.PreferredNodeSelector{
				{Weight: 10, Preference: metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "x",
					Operator: "BadOperator",
				}}}},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			ns := tc.nodes
			if ns == nil {
				ns = nodes
			}
			got, err := preferredNodeScores(ns, tc.eligible, tc.preferred)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("preferredNodeScores returned error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("preferredNodeScores mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestL2AdvertisementFromCR(t *testing.T) {
	edgeNodes := []corev1.Node{
		node("edge-a", map[string]string{"role": "edge"}),
		node("edge-b", map[string]string{"role": "edge"}),
		node("worker-a", map[string]string{"role": "worker"}),
	}

	tests := []struct {
		desc             string
		nodes            []corev1.Node
		crd              v1beta1.L2Advertisement
		wantNodes        map[string]bool
		wantPreferred    map[string]int64
		wantSvcSelectors int
	}{
		{
			desc:  "preferred selector scores matching nodes",
			nodes: edgeNodes,
			crd: v1beta1.L2Advertisement{
				ObjectMeta: metav1.ObjectMeta{Name: "prefer-edge"},
				Spec: v1beta1.L2AdvertisementSpec{
					PreferredNodeSelectors: []v1beta1.PreferredNodeSelector{
						{Weight: 100, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "edge"}}},
					},
				},
			},
			wantNodes:     map[string]bool{"edge-a": true, "edge-b": true, "worker-a": true},
			wantPreferred: map[string]int64{"edge-a": 100, "edge-b": 100},
		},
		{
			desc:  "service selectors do not restrict preferred scoring",
			nodes: edgeNodes,
			crd: v1beta1.L2Advertisement{
				ObjectMeta: metav1.ObjectMeta{Name: "prefer-edge-with-svc-selectors"},
				Spec: v1beta1.L2AdvertisementSpec{
					ServiceSelectors: []metav1.LabelSelector{
						{MatchLabels: map[string]string{"app": "web"}},
					},
					PreferredNodeSelectors: []v1beta1.PreferredNodeSelector{
						{Weight: 100, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "edge"}}},
					},
				},
			},
			wantPreferred:    map[string]int64{"edge-a": 100, "edge-b": 100},
			wantSvcSelectors: 1,
		},
		{
			desc: "no preferred selectors leaves PreferredNodes nil",
			crd: v1beta1.L2Advertisement{
				ObjectMeta: metav1.ObjectMeta{Name: "basic"},
				Spec:       v1beta1.L2AdvertisementSpec{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := l2AdvertisementFromCR(tc.crd, tc.nodes)
			if err != nil {
				t.Fatalf("l2AdvertisementFromCR returned error: %v", err)
			}
			if tc.wantNodes != nil {
				if diff := cmp.Diff(tc.wantNodes, got.Nodes); diff != "" {
					t.Fatalf("Nodes mismatch (-want +got):\n%s", diff)
				}
			}
			if diff := cmp.Diff(tc.wantPreferred, got.PreferredNodes); diff != "" {
				t.Fatalf("PreferredNodes mismatch (-want +got):\n%s", diff)
			}
			if tc.wantSvcSelectors > 0 && len(got.ServiceSelectors) != tc.wantSvcSelectors {
				t.Fatalf("ServiceSelectors: want %d, got %d", tc.wantSvcSelectors, len(got.ServiceSelectors))
			}
		})
	}
}

func TestSetL2AdvertisementsToPools(t *testing.T) {
	tests := []struct {
		desc    string
		pools   []v1beta1.IPAddressPool
		advs    []v1beta1.L2Advertisement
		wantLen int
	}{
		{
			desc: "pool selected by name and selector attaches once",
			pools: []v1beta1.IPAddressPool{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool-a", Labels: map[string]string{"shared": "true"}},
					Spec:       v1beta1.IPAddressPoolSpec{Addresses: []string{"10.20.0.0/24"}},
				},
			},
			advs: []v1beta1.L2Advertisement{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prefer-edge"},
					Spec: v1beta1.L2AdvertisementSpec{
						IPAddressPools: []string{"pool-a"},
						IPAddressPoolSelectors: []metav1.LabelSelector{
							{MatchLabels: map[string]string{"shared": "true"}},
						},
						PreferredNodeSelectors: []v1beta1.PreferredNodeSelector{
							{Weight: 50, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "edge"}}},
						},
					},
				},
			},
			wantLen: 1,
		},
		{
			desc: "two CRs targeting the same pool attach independently",
			pools: []v1beta1.IPAddressPool{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool-a"},
					Spec:       v1beta1.IPAddressPoolSpec{Addresses: []string{"10.20.0.0/24"}},
				},
			},
			advs: []v1beta1.L2Advertisement{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "ad1-edge-70"},
					Spec: v1beta1.L2AdvertisementSpec{
						IPAddressPools: []string{"pool-a"},
						PreferredNodeSelectors: []v1beta1.PreferredNodeSelector{
							{Weight: 70, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "edge"}}},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "ad2-edge-30"},
					Spec: v1beta1.L2AdvertisementSpec{
						IPAddressPools: []string{"pool-a"},
						PreferredNodeSelectors: []v1beta1.PreferredNodeSelector{
							{Weight: 30, Preference: metav1.LabelSelector{MatchLabels: map[string]string{"role": "edge"}}},
						},
					},
				},
			},
			wantLen: 2,
		},
	}

	nodes := []corev1.Node{node("edge-a", map[string]string{"role": "edge"})}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			poolMap := map[string]*Pool{"pool-a": {Name: "pool-a"}}
			if err := setL2AdvertisementsToPools(tc.pools, tc.advs, nodes, poolMap); err != nil {
				t.Fatalf("setL2AdvertisementsToPools: %v", err)
			}
			if got := len(poolMap["pool-a"].L2Advertisements); got != tc.wantLen {
				t.Fatalf("pool-a: want %d L2Advertisements, got %d", tc.wantLen, got)
			}
		})
	}
}
