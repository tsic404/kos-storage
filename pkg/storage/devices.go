package storage

import (
	"context"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/device"
	"github.com/tsic404/kosd/pkg/pages/cluster"
	"github.com/tsic404/kosd/pkg/provider"
)

// storageClasses is the native class list a storage policy is read from.
var storageClasses = schema.GroupVersionResource{
	Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses",
}

// devicesAndPoliciesView is the item STORAGE-C-1 names: device facts, the
// native storage classes, and the providers that registered them.
type devicesAndPoliciesView struct {
	Devices        []deviceView       `json:"devices"`
	Warnings       []string           `json:"warnings,omitempty"`
	StorageClasses []storageClassView `json:"storageClasses"`
	Providers      []providerView     `json:"providers"`
	ReadOnly       []reasonView       `json:"readOnly"`
}

// deviceView is one device as the page shows it.
type deviceView struct {
	Node               string `json:"node"`
	Name               string `json:"name"`
	Serial             string `json:"serial,omitempty"`
	Model              string `json:"model,omitempty"`
	Kind               string `json:"kind,omitempty"`
	Present            bool   `json:"present"`
	Recognized         bool   `json:"recognized"`
	DriverReady        bool   `json:"driverReady"`
	Published          bool   `json:"published"`
	Occupied           bool   `json:"occupied"`
	OccupancySource    string `json:"occupancySource,omitempty"`
	OccupancyReference string `json:"occupancyReference,omitempty"`
	Stale              bool   `json:"stale"`
	Invalid            bool   `json:"invalid"`
	Allocatable        bool   `json:"allocatable"`
}

// storageClassView is one native storage class as the page shows it.
type storageClassView struct {
	Name                 string `json:"name"`
	Provisioner          string `json:"provisioner,omitempty"`
	ReclaimPolicy        string `json:"reclaimPolicy,omitempty"`
	AllowVolumeExpansion bool   `json:"allowVolumeExpansion"`
	VolumeBindingMode    string `json:"volumeBindingMode,omitempty"`
}

// providerView is one registered provider with the state KOSD-C-47 writes and
// the dependency edges KOSD-C-48 makes observable.
type providerView struct {
	Name                  string           `json:"name"`
	Component             string           `json:"component,omitempty"`
	Version               string           `json:"version,omitempty"`
	StorageClasses        []string         `json:"storageClasses"`
	Phase                 string           `json:"phase,omitempty"`
	Health                string           `json:"health"`
	Registered            bool             `json:"registered"`
	Verified              bool             `json:"verified"`
	Ready                 bool             `json:"ready"`
	UpgradeBlocked        bool             `json:"upgradeBlocked"`
	DeprovisionBlocked    bool             `json:"deprovisionBlocked"`
	Conditions            []conditionView  `json:"conditions"`
	Capabilities          []capabilityView `json:"capabilities"`
	EffectiveCapabilities []capabilityView `json:"effectiveCapabilities"`
	Dependents            []referenceView  `json:"dependents"`
	Referrers             []referenceView  `json:"referrers"`
	ReadOnly              []reasonView     `json:"readOnly"`
}

// conditionView is one status condition as the page shows it.
type conditionView struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// capabilityView is one registered or effective capability.
type capabilityView struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	State   string `json:"state,omitempty"`
	Source  string `json:"source,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// referenceView is one object reference a provider lists.
type referenceView struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// devicesAndPolicies reads the device facts, storage classes and providers
// (STORAGE-C-1, STORAGE-C-82).
func (r *Readings) devicesAndPolicies(ctx context.Context) (any, *controlplane.Error) {
	facts, failure := r.reader().List(ctx, "NodeDevice", "")
	if failure != nil {
		return nil, failure
	}
	objects := make([]*unstructured.Unstructured, 0, len(facts))
	for _, fact := range facts {
		objects = append(objects, &unstructured.Unstructured{Object: fact})
	}
	view := device.BuildView(objects, r.reader().Clock())
	devices := make([]deviceView, 0, len(view.Devices))
	for _, entry := range view.Devices {
		devices = append(devices, deviceView{
			Node: entry.Node, Name: entry.Name, Serial: entry.Serial, Model: entry.Model, Kind: entry.Kind,
			Present: entry.IsPresent, Recognized: entry.IsRecognized, DriverReady: entry.IsDriverReady,
			Published: entry.IsPublished, Occupied: entry.IsOccupied, OccupancySource: entry.OccupancySource,
			OccupancyReference: entry.OccupancyReference, Stale: entry.IsStale, Invalid: entry.IsInvalid,
			Allocatable: entry.IsAllocatable,
		})
	}
	classes, failure := r.classes(ctx)
	if failure != nil {
		return nil, failure
	}
	providers, failure := r.providers(ctx)
	if failure != nil {
		return nil, failure
	}
	return devicesAndPoliciesView{
		Devices: devices, Warnings: view.Warnings, StorageClasses: classes, Providers: providers,
		ReadOnly: []reasonView{
			{Item: "devices", Reason: "设备事实由采集适配器写入；本页只读展示"},
			{Item: "storageClasses", Reason: "存储类由 Provider 注册声明；本页只读展示"},
			{Item: "providers", Reason: "Provider 的注册、校验与退场状态由注册路径与校验器写入；本页只读展示"},
		},
	}, nil
}

// classes reads the native storage classes.
func (r *Readings) classes(ctx context.Context) ([]storageClassView, *controlplane.Error) {
	list, err := r.Dynamic.Resource(storageClasses).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, controlplaneFailure(err, "storageclasses")
	}
	classes := make([]storageClassView, 0, len(list.Items))
	for index := range list.Items {
		item := list.Items[index]
		classes = append(classes, storageClassView{
			Name:                 item.GetName(),
			Provisioner:          cluster.Text(item.Object, "provisioner"),
			ReclaimPolicy:        cluster.Text(item.Object, "reclaimPolicy"),
			AllowVolumeExpansion: boolField(item.Object, "allowVolumeExpansion"),
			VolumeBindingMode:    cluster.Text(item.Object, "volumeBindingMode"),
		})
	}
	sort.Slice(classes, func(left, right int) bool { return classes[left].Name < classes[right].Name })
	return classes, nil
}

// providers reads the registered providers (KOSD-C-47) and their dependency
// edges (KOSD-C-48).
func (r *Readings) providers(ctx context.Context) ([]providerView, *controlplane.Error) {
	objects, failure := r.reader().List(ctx, "Provider", "")
	if failure != nil {
		return nil, failure
	}
	views := make([]providerView, 0, len(objects))
	for _, object := range objects {
		var stored v1beta1.Provider
		if failure := cluster.Decode(object, &stored); failure != nil {
			return nil, failure
		}
		views = append(views, providerOf(stored))
	}
	sort.Slice(views, func(left, right int) bool { return views[left].Name < views[right].Name })
	return views, nil
}

// providerOf renders one provider's registration, verification and dependency
// state.
func providerOf(stored v1beta1.Provider) providerView {
	view := providerView{
		Name:                  stored.Name,
		Component:             stored.Spec.ComponentRef,
		Version:               stored.Spec.Version,
		StorageClasses:        append([]string(nil), stored.Spec.StorageClasses...),
		Phase:                 stored.Status.Phase,
		Health:                string(stored.Status.Health.State),
		Registered:            conditionIsTrue(stored.Status.Conditions, provider.ConditionRegistered),
		Verified:              conditionIsTrue(stored.Status.Conditions, provider.ConditionVerified),
		Ready:                 conditionIsTrue(stored.Status.Conditions, provider.ConditionReady),
		UpgradeBlocked:        conditionIsTrue(stored.Status.Conditions, provider.ConditionUpgradeBlocked),
		DeprovisionBlocked:    conditionIsTrue(stored.Status.Conditions, provider.ConditionDeprovisionBlocked),
		Conditions:            conditionsOf(stored.Status.Conditions),
		Capabilities:          capabilityResultsOf(stored.Status.Capabilities),
		EffectiveCapabilities: capabilitySetOf(stored.Status.EffectiveCapabilities),
		Dependents:            referencesOf(stored.Status.Dependents),
		Referrers:             referencesOf(stored.Status.References),
		ReadOnly: []reasonView{
			{Item: "provider", Reason: "Provider 的注册与校验状态由注册路径与校验器写入；本页只读展示"},
			{Item: "storageClasses", Reason: "存储类由 Provider 注册声明；本页只读展示"},
		},
	}
	return view
}

// conditionsOf renders stored conditions in declaration order.
func conditionsOf(conditions []metav1.Condition) []conditionView {
	views := make([]conditionView, 0, len(conditions))
	for _, condition := range conditions {
		view := conditionView{
			Type: condition.Type, Status: string(condition.Status),
			Reason: condition.Reason, Message: condition.Message,
		}
		if !condition.LastTransitionTime.IsZero() {
			view.LastTransitionTime = condition.LastTransitionTime.UTC().Format(timeLayout)
		}
		views = append(views, view)
	}
	return views
}

// capabilityResultsOf renders the verification results of a provider.
func capabilityResultsOf(results []v1beta1.ProviderCapabilityResult) []capabilityView {
	views := make([]capabilityView, 0, len(results))
	for _, result := range results {
		views = append(views, capabilityView{
			Name: result.Name, Version: result.Version, State: result.State, Source: result.Source, Reason: result.Reason,
		})
	}
	return views
}

// capabilitySetOf renders a capability set.
func capabilitySetOf(capabilities []v1beta1.ProviderCapability) []capabilityView {
	views := make([]capabilityView, 0, len(capabilities))
	for _, capability := range capabilities {
		views = append(views, capabilityView{Name: capability.Name, Version: capability.Version})
	}
	return views
}

// referencesOf renders the objects a provider lists.
func referencesOf(refs []v1beta1.ObjectRef) []referenceView {
	views := make([]referenceView, 0, len(refs))
	for _, ref := range refs {
		views = append(views, referenceView{Kind: ref.Kind, Namespace: ref.Namespace, Name: ref.Name})
	}
	return views
}

// conditionIsTrue reports whether one condition is present and True.
func conditionIsTrue(conditions []metav1.Condition, conditionType string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}

// boolField reads one boolean field of a stored object.
func boolField(object map[string]any, path ...string) bool {
	value, _, _ := unstructured.NestedBool(object, path...)
	return value
}
