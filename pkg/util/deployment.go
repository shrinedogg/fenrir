package util

import (
	appsv1 "k8s.io/api/apps/v1"
)

// DeploymentReady reports whether the deployment controller has observed the
// latest generation and the desired number of replicas (spec.replicas,
// default 1) are all ready.
//
// Comparing only status.readyReplicas with status.replicas is not enough: a
// freshly created deployment reports 0/0 for a moment after its generation is
// observed and before the ReplicaSet counts a pod, and 0 == 0 would report it
// ready with no pod behind it.
func DeploymentReady(d *appsv1.Deployment) bool {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	return d.Status.ObservedGeneration == d.Generation &&
		d.Status.ReadyReplicas >= desired &&
		d.Status.ReadyReplicas == d.Status.Replicas
}
