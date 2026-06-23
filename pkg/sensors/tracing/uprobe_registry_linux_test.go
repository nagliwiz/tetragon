// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Tetragon

//go:build !windows

package tracing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// T5: the registry routes container add/del events to the reconciler
// registered for the matching policy, and ignores events for unknown policies.
func TestReconcilerRegistryRouting(t *testing.T) {
	reg := newUprobeReconcilerRegistry()

	attA := newFakeAttacher()
	rA := newContainerUprobeReconciler("/procRoot", []ricTarget{{path: "/lib/a.so"}}, attA, fakeResolveRoot())
	attB := newFakeAttacher()
	rB := newContainerUprobeReconciler("/procRoot", []ricTarget{{path: "/lib/b.so"}}, attB, fakeResolveRoot())

	reg.register("policyA", rA)
	reg.register("policyB", rB)

	reg.containerAdded("policyA", "pod1/c1")
	reg.containerAdded("policyB", "pod2/c2")
	// event for an unknown policy must be ignored, not panic.
	reg.containerAdded("policyZ", "pod3/c3")

	require.Equal(t, []string{"pod1/c1"}, attA.attachedKeys())
	require.Equal(t, []string{"pod2/c2"}, attB.attachedKeys())

	reg.containerDeleted("policyA", "pod1/c1")
	require.Empty(t, attA.attachedKeys())
	require.Equal(t, []string{"pod2/c2"}, attB.attachedKeys())
}

// Wiring-3: matchingPolicies returns policies whose matcher selects the pod; a
// nil matcher matches all pods.
func TestReconcilerRegistryMatchingPolicies(t *testing.T) {
	reg := newUprobeReconcilerRegistry()
	att := newFakeAttacher()

	// policyAll: nil matcher -> matches everything.
	reg.registerWithMatcher("policyAll", newContainerUprobeReconciler("/p", []ricTarget{{path: "/a"}}, att, fakeResolveRoot()), nil)
	// policySshd: matches only namespace "prod" with label app=sshd.
	reg.registerWithMatcher("policySshd", newContainerUprobeReconciler("/p", []ricTarget{{path: "/b"}}, att, fakeResolveRoot()),
		func(ns string, lbls map[string]string) bool {
			return ns == "prod" && lbls["app"] == "sshd"
		})

	require.ElementsMatch(t, []string{"policyAll", "policySshd"},
		reg.matchingPolicies("prod", map[string]string{"app": "sshd"}))
	require.ElementsMatch(t, []string{"policyAll"},
		reg.matchingPolicies("dev", map[string]string{"app": "sshd"}))
	require.ElementsMatch(t, []string{"policyAll"},
		reg.matchingPolicies("prod", map[string]string{"app": "nginx"}))
}

// T6: registering a policy then snapshotting existing containers attaches them,
// and subsequent live events still route correctly. This mirrors the production
// path (registerWithMatcher + snapshot of cached containers).
func TestReconcilerRegistrySnapshotOnRegister(t *testing.T) {
	reg := newUprobeReconcilerRegistry()
	att := newFakeAttacher()
	r := newContainerUprobeReconciler("/procRoot", []ricTarget{{path: "/lib/a.so"}}, att, fakeResolveRoot())

	reg.register("policyA", r)
	r.snapshot([]matchedContainer{
		{key: "pod1/c1"},
		{key: "pod1/c2"},
	})

	require.ElementsMatch(t, []string{"pod1/c1", "pod1/c2"}, att.attachedKeys())

	// subsequent live events still route correctly after the snapshot.
	reg.containerAdded("policyA", "pod2/c3")
	require.Len(t, att.attachedKeys(), 3)
}

// T5: unregistering a policy detaches all of its containers and stops routing.
func TestReconcilerRegistryUnregisterDetachesAll(t *testing.T) {
	reg := newUprobeReconcilerRegistry()
	att := newFakeAttacher()
	r := newContainerUprobeReconciler("/procRoot", []ricTarget{{path: "/lib/a.so"}}, att, fakeResolveRoot())

	reg.register("policyA", r)
	reg.containerAdded("policyA", "pod1/c1")
	reg.containerAdded("policyA", "pod1/c2")
	require.Len(t, att.attachedKeys(), 2)

	reg.unregister("policyA")
	// unregister detaches asynchronously (it can run while the sensor manager
	// holds its collection lock), so wait for the detaches to land.
	require.Eventually(t, func() bool { return len(att.attachedKeys()) == 0 },
		time.Second, time.Millisecond, "unregister must detach all containers")

	// further events for the unregistered policy are ignored.
	reg.containerAdded("policyA", "pod1/c3")
	require.Empty(t, att.attachedKeys())
}
