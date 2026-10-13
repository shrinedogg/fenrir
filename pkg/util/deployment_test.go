package util_test

import (
	"testing"

	"games-on-whales.github.io/direwolf/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func deployment(generation, observed int64, specReplicas *int32, replicas, ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Generation: generation},
		Spec:       appsv1.DeploymentSpec{Replicas: specReplicas},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: observed,
			Replicas:           replicas,
			ReadyReplicas:      ready,
		},
	}
}

func TestDeploymentReady(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    *appsv1.Deployment
		want bool
	}{
		{
			// The deployment controller has observed the new object but the
			// ReplicaSet has not reported a pod yet. Treating 0/0 as ready
			// made the session controller dial the session Service before
			// Cilium had programmed it; the connect hung ~130s and the
			// 1-minute reaper deleted the session.
			name: "fresh deployment, status 0/0",
			d:    deployment(1, 1, ptr.To[int32](1), 0, 0),
			want: false,
		},
		{
			name: "fresh deployment, spec.replicas unset (defaults to 1), status 0/0",
			d:    deployment(1, 1, nil, 0, 0),
			want: false,
		},
		{
			name: "generation not observed yet",
			d:    deployment(1, 0, ptr.To[int32](1), 0, 0),
			want: false,
		},
		{
			name: "pod created, not ready",
			d:    deployment(1, 1, ptr.To[int32](1), 1, 0),
			want: false,
		},
		{
			name: "pod ready",
			d:    deployment(1, 1, ptr.To[int32](1), 1, 1),
			want: true,
		},
		{
			name: "rollout surge: one of two pods ready",
			d:    deployment(2, 2, ptr.To[int32](1), 2, 1),
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := util.DeploymentReady(tc.d); got != tc.want {
				t.Errorf("DeploymentReady() = %v, want %v (status %+v)", got, tc.want, tc.d.Status)
			}
		})
	}
}
