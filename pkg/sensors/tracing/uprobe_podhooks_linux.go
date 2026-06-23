// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Tetragon

//go:build !windows && !nok8s

package tracing

import (
	v1 "k8s.io/api/core/v1"

	"github.com/cilium/tetragon/pkg/manager/events"
	"github.com/cilium/tetragon/pkg/podhelpers"
)

// containerKey returns a stable identifier for a container within a pod, used
// as the reconciler attach key. It combines the pod UID with the container id
// so the same key can be reconstructed on add and delete. Both inputs are
// "/"-free (pod UIDs are UUIDs, container ids are runtime-prefix-stripped hex),
// so the separator is unambiguous.
func containerKey(podUID, containerID string) string {
	return podUID + "/" + containerID
}

// uprobePodHandlers translates pod add/update/delete events into
// reconciler-registry calls. Container->policy matching reuses the same selector
// machinery as policyfilter; adds route every container in matching pods through
// the registry, which only acts on policies that are actually registered
// (resolvePathInContainer uprobes). Per-container root resolution (runtime hook /
// CRI) happens later in the reconciler, keyed by container id.
//
// The cache.DeletedFinalStateUnknown unwrap and the *v1.Pod type assertion
// happen in the pod-event adapter (see pkg/manager), so these callbacks deal
// only with concrete *v1.Pod values.
type uprobePodHandlers struct {
	reg           *uprobeReconcilerRegistry
	matchPolicies func(pod *v1.Pod) []string
}

func newUprobePodHandlers(reg *uprobeReconcilerRegistry, matchPolicies func(pod *v1.Pod) []string) *uprobePodHandlers {
	return &uprobePodHandlers{reg: reg, matchPolicies: matchPolicies}
}

// register wires the handlers into the supplied pod-event source. It registers
// no new informer of its own — the source is the shared pod informer, the same
// one policyfilter attaches to.
func (h *uprobePodHandlers) register(src events.PodEventSource) error {
	if err := src.OnPodAdd(h.onAdd); err != nil {
		return err
	}
	if err := src.OnPodUpdate(h.onUpdate); err != nil {
		return err
	}
	return src.OnPodDelete(h.onDelete)
}

func (h *uprobePodHandlers) onAdd(pod *v1.Pod) {
	policies := h.matchPolicies(pod)
	if len(policies) == 0 {
		return
	}
	uid := string(pod.UID)
	for _, cid := range podhelpers.PodContainersIDs(pod) {
		key := containerKey(uid, cid)
		for _, pol := range policies {
			h.reg.containerAdded(pol, key)
		}
	}
}

func (h *uprobePodHandlers) onUpdate(oldPod, newPod *v1.Pod) {
	// Detach containers that were running in the old pod but are no longer
	// running in the new one (terminated/removed without the pod being
	// deleted), otherwise their sensors leak until policy removal.
	newKeys := make(map[string]struct{})
	for _, k := range h.containerKeys(newPod) {
		newKeys[k] = struct{}{}
	}
	var removed []string
	for _, k := range h.containerKeys(oldPod) {
		if _, present := newKeys[k]; !present {
			removed = append(removed, k)
		}
	}
	h.detachKeys(removed)
	h.onAdd(newPod)
}

func (h *uprobePodHandlers) onDelete(pod *v1.Pod) {
	h.detachKeys(h.containerKeys(pod))
}

// detachKeys routes a detach for each key to every registered policy; unknown
// keys are no-ops in each reconciler.
func (h *uprobePodHandlers) detachKeys(keys []string) {
	policies := h.reg.policies()
	for _, key := range keys {
		for _, pol := range policies {
			h.reg.containerDeleted(pol, key)
		}
	}
}

func (h *uprobePodHandlers) containerKeys(pod *v1.Pod) []string {
	uid := string(pod.UID)
	cids := podhelpers.PodContainersIDs(pod)
	keys := make([]string, 0, len(cids))
	for _, cid := range cids {
		keys = append(keys, containerKey(uid, cid))
	}
	return keys
}
