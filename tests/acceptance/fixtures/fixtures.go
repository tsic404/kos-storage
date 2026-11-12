// Package fixtures builds the objects the storage acceptance cases read, so
// every case shapes the same bounded volume and backup state.
package fixtures

import (
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/tests/acceptance/harness"
)

// Provider builds one registered provider; a provider that did not pass its
// conformance run carries Verified=False and a degraded capability result.
func Provider(name string, classes []string, isVerified bool) *unstructured.Unstructured {
	verified := condition("Verified", "True", "Verified")
	verifiedState := "Verified"
	ready := "True"
	phase := "Ready"
	if !isVerified {
		verified = condition("Verified", "False", "ConformanceFailed")
		verified["message"] = "capability storage.volume failed its case set"
		verifiedState = "Degraded"
		ready = "False"
		phase = "Degraded"
	}
	capabilityResults := []any{map[string]any{
		"name": "storage.volume", "version": "v1", "source": "KOS", "state": verifiedState,
	}}
	if !isVerified {
		capabilityResults[0].(map[string]any)["reason"] = "case set failed"
		capabilityResults[0].(map[string]any)["failedCases"] = []any{"volume-write"}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": v1beta1.GroupVersion.String(),
		"kind":       "Provider",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"componentRef": "kos-storage", "version": "1.0.0",
			"storageClasses": stringsOf(classes),
		},
		"status": map[string]any{
			"phase": phase, "health": map[string]any{"state": healthOf(isVerified)},
			"conditions": []any{
				condition("Registered", "True", "Registered"),
				verified,
				condition("Ready", ready, "Ready"),
				condition("UpgradeBlocked", "False", "NoCapabilityWithdrawn"),
			},
			"capabilities": capabilityResults,
		},
	}}
}

// DeviceFact builds one node's device fact.
func DeviceFact(node string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": v1beta1.GroupVersion.String(),
		"kind":       "NodeDevice",
		"metadata":   map[string]any{"name": node},
		"spec": map[string]any{
			"node": node, "collector": node, "observedAt": now(),
			"devices": []any{map[string]any{
				"name": "nvme0", "serial": "S1", "model": "Acceptance", "kind": "disk",
				"present": true, "recognized": true, "driverReady": true, "published": true,
				"observedAt": now(),
			}},
		},
	}}
}

// BackupPolicy builds one scheduled policy with a completed run and drill.
func BackupPolicy(namespace, name string) *unstructured.Unstructured {
	moment := time.Now().UTC()
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": v1beta1.GroupVersion.String(),
		"kind":       "BackupPolicy",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec": map[string]any{
			"schedule": "0 2 * * *", "consistency": "crash-consistent",
			"target": map[string]any{"path": "/backup", "failureDomain": "disk-b"},
			"drill":  map[string]any{"enabled": true},
		},
		"status": map[string]any{
			"phase": "Scheduled", "health": map[string]any{"state": "Healthy"},
			"lastBackup": map[string]any{
				"completedAt": moment.Add(-time.Hour).Format(time.RFC3339),
				"consistency": "crash-consistent", "succeeded": true,
			},
			"lastDrill": map[string]any{
				"phase": "Succeeded", "completedAt": moment.Add(-30 * time.Minute).Format(time.RFC3339),
				"succeeded": true,
			},
		},
	}}
}

// Volume builds one stored volume; the caller keeps spec and status explicit.
func Volume(namespace, name, owner string, spec, status map[string]any) *unstructured.Unstructured {
	object := harness.Volume(namespace, name, spec)
	if owner != "" {
		object.Object["spec"].(map[string]any)["owner"] = owner
	}
	if status != nil {
		object.Object["status"] = status
	}
	return object
}

// Migration builds one attempt of a volume with the stage state a case wants to
// read; a running attempt carries the rollback point of its stage.
func Migration(namespace, volume, targetClass, phase string, isRunning bool) *unstructured.Unstructured {
	status := map[string]any{
		"phase": phase, "sourceClaim": volume, "targetClaim": volume + "-target",
	}
	if isRunning {
		status["rollbackPoint"] = phase
		status["windowStartedAt"] = now()
	}
	object := harness.Migration(namespace, volume, map[string]any{
		"volume": volume, "targetStorageClass": targetClass,
		"window": map[string]any{"duration": "30m"},
	})
	object.Object["status"] = status
	return object
}

// condition is one stored status condition.
func condition(conditionType, status, reason string) map[string]any {
	return map[string]any{
		"type": conditionType, "status": status, "reason": reason,
		"message": "acceptance", "lastTransitionTime": now(),
	}
}

// stringsOf renders a string list as the stored list form.
func stringsOf(values []string) []any {
	list := make([]any, 0, len(values))
	for _, value := range values {
		list = append(list, value)
	}
	return list
}

// healthOf picks the health a provider records for its verification result.
func healthOf(isVerified bool) string {
	if isVerified {
		return "Healthy"
	}
	return "Degraded"
}

// now is the acceptance timestamp.
func now() string { return time.Now().UTC().Format(time.RFC3339) }
