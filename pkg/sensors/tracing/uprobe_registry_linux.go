// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Tetragon

//go:build !windows

package tracing

import "sync"

// uprobeReconcilerRegistry holds the per-policy containerUprobeReconcilers for
// resolvePathInContainer uprobe policies, and routes pod/container lifecycle
// events to the right reconciler.
//
// It is the single subscriber to pod events (wired onto the shared pod
// informer, so no second informer is created): the pod-event handler resolves
// which policies a container matches, then calls containerAdded /
// containerDeleted here. Events for policies that are not registered are
// ignored.
// podMatcher reports whether a pod (by namespace and labels) is selected by a
// policy. It is set when a policy is registered so the pod-event handler can
// determine which policies a pod matches.
type podMatcher func(namespace string, labels map[string]string) bool

type registeredReconciler struct {
	r     *containerUprobeReconciler
	match podMatcher
}

type uprobeReconcilerRegistry struct {
	mu          sync.RWMutex
	reconcilers map[string]*registeredReconciler // policy long-name -> reconciler
}

func newUprobeReconcilerRegistry() *uprobeReconcilerRegistry {
	return &uprobeReconcilerRegistry{
		reconcilers: map[string]*registeredReconciler{},
	}
}

// register associates a reconciler with a policy and a nil matcher (matches all
// pods). Convenience wrapper around registerWithMatcher.
func (reg *uprobeReconcilerRegistry) register(policy string, r *containerUprobeReconciler) {
	reg.registerWithMatcher(policy, r, nil)
}

// registerWithMatcher associates a reconciler with a policy. Called when a
// resolvePathInContainer uprobe policy is loaded. match may be nil (no pod-level
// filtering), in which case the policy matches all pods.
func (reg *uprobeReconcilerRegistry) registerWithMatcher(policy string, r *containerUprobeReconciler, match podMatcher) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.reconcilers[policy] = &registeredReconciler{r: r, match: match}
}

// matchingPolicies returns the names of registered policies whose pod matcher
// selects the given pod. A policy with a nil matcher matches all pods.
func (reg *uprobeReconcilerRegistry) matchingPolicies(namespace string, labels map[string]string) []string {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	var out []string
	for name, rr := range reg.reconcilers {
		if rr.match == nil || rr.match(namespace, labels) {
			out = append(out, name)
		}
	}
	return out
}

// unregister removes a policy's reconciler and detaches all of its containers.
// Called when the policy is removed.
func (reg *uprobeReconcilerRegistry) unregister(policy string) {
	reg.mu.Lock()
	rr := reg.reconcilers[policy]
	delete(reg.reconcilers, policy)
	reg.mu.Unlock()
	if rr != nil {
		rr.r.detachAll()
	}
}

func (reg *uprobeReconcilerRegistry) get(policy string) *containerUprobeReconciler {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	if rr := reg.reconcilers[policy]; rr != nil {
		return rr.r
	}
	return nil
}

// policies returns the names of the currently registered policies.
func (reg *uprobeReconcilerRegistry) policies() []string {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	out := make([]string, 0, len(reg.reconcilers))
	for p := range reg.reconcilers {
		out = append(out, p)
	}
	return out
}

// containerAdded routes a container-add event to the reconciler for policy.
// Unknown policies are ignored.
func (reg *uprobeReconcilerRegistry) containerAdded(policy, key string) {
	if r := reg.get(policy); r != nil {
		r.onContainerAdd(matchedContainer{key: key})
	}
}

// containerDeleted routes a container-delete event to the reconciler for
// policy. Unknown policies are ignored.
func (reg *uprobeReconcilerRegistry) containerDeleted(policy, key string) {
	if r := reg.get(policy); r != nil {
		r.onContainerDel(key)
	}
}
