/*
Copyright 2026 The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package state

import (
	"errors"
	"testing"

	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"
	"volcano.sh/apis/pkg/apis/bus/v1alpha1"
)

// ── action-agnostic behaviour ────────────────────────────────────────────────

// TestCompletingState_Execute_ActionAgnostic verifies that Execute always
// delegates to KillJob regardless of the action type received.
func TestCompletingState_Execute_ActionAgnostic(t *testing.T) {
	actions := []struct {
		name   string
		action v1alpha1.Action
	}{
		{"SyncJobAction", v1alpha1.SyncJobAction},
		{"RestartJobAction", v1alpha1.RestartJobAction},
		{"AbortJobAction", v1alpha1.AbortJobAction},
		{"TerminateJobAction", v1alpha1.TerminateJobAction},
		{"CompleteJobAction", v1alpha1.CompleteJobAction},
		{"ResumeJobAction", v1alpha1.ResumeJobAction},
		{"RestartTaskAction", v1alpha1.RestartTaskAction},
		{"RestartPodAction", v1alpha1.RestartPodAction},
		{"RestartPartitionAction", v1alpha1.RestartPartitionAction},
		{"UnknownAction", v1alpha1.Action("Unknown")},
	}

	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			cap := captureKillJob(t, nil)
			s := &completingState{job: makeJobInfo(vcbatch.Completing)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if cap.job == nil {
				t.Fatal("KillJob was not called")
			}
		})
	}
}

// ── KillJob call arguments ───────────────────────────────────────────────────

// TestCompletingState_Execute_UsesSoftRetainPhase verifies that Execute passes
// PodRetainPhaseSoft so Succeeded/Failed pods are preserved during draining.
func TestCompletingState_Execute_UsesSoftRetainPhase(t *testing.T) {
	cap := captureKillJob(t, nil)
	s := &completingState{job: makeJobInfo(vcbatch.Completing)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for phase := range PodRetainPhaseSoft {
		if _, ok := cap.podRetainPhase[phase]; !ok {
			t.Errorf("podRetainPhase missing %q", phase)
		}
	}
	if len(cap.podRetainPhase) != len(PodRetainPhaseSoft) {
		t.Errorf("podRetainPhase has %d entries, want %d", len(cap.podRetainPhase), len(PodRetainPhaseSoft))
	}
}

// TestCompletingState_Execute_NonNilUpdateFn verifies the updateFn is not nil
// — completingState drives a phase transition, unlike finishedState.
func TestCompletingState_Execute_NonNilUpdateFn(t *testing.T) {
	cap := captureKillJob(t, nil)
	s := &completingState{job: makeJobInfo(vcbatch.Completing)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.updateFn == nil {
		t.Error("updateFn must not be nil for completingState")
	}
}

// TestCompletingState_Execute_PassesJobInfo verifies the correct JobInfo
// pointer is forwarded to KillJob.
func TestCompletingState_Execute_PassesJobInfo(t *testing.T) {
	cap := captureKillJob(t, nil)
	info := makeJobInfo(vcbatch.Completing)
	s := &completingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.job != info {
		t.Errorf("KillJob received wrong JobInfo: got %p, want %p", cap.job, info)
	}
}

// TestCompletingState_Execute_PropagatesError verifies that any error returned
// by KillJob is surfaced to the caller unchanged.
func TestCompletingState_Execute_PropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &completingState{job: makeJobInfo(vcbatch.Completing)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

// ── updateFn: alive pods present → stay Completing ───────────────────────────

// TestCompletingState_Execute_UpdateFnAlivePods verifies that the updateFn
// returns false and leaves the phase unchanged whenever any "alive" pod counter
// (Terminating, Pending, or Running) is non-zero.
func TestCompletingState_Execute_UpdateFnAlivePods(t *testing.T) {
	tests := []struct {
		name   string
		status vcbatch.JobStatus
	}{
		{
			name:   "only Terminating pods remain",
			status: vcbatch.JobStatus{Terminating: 1},
		},
		{
			name:   "only Pending pods remain",
			status: vcbatch.JobStatus{Pending: 1},
		},
		{
			name:   "only Running pods remain",
			status: vcbatch.JobStatus{Running: 1},
		},
		{
			name:   "all three counters non-zero",
			status: vcbatch.JobStatus{Terminating: 2, Pending: 3, Running: 1},
		},
		{
			name:   "Terminating and Pending non-zero",
			status: vcbatch.JobStatus{Terminating: 1, Pending: 1},
		},
		{
			name:   "Pending and Running non-zero",
			status: vcbatch.JobStatus{Pending: 1, Running: 1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cap := captureKillJob(t, nil)
			s := &completingState{job: makeJobInfo(vcbatch.Completing)}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			originalPhase := tc.status.State.Phase
			changed := cap.updateFn(&tc.status)

			if changed {
				t.Error("updateFn should return false while alive pods exist")
			}
			if tc.status.State.Phase != originalPhase {
				t.Errorf("phase should not change: got %q, want %q", tc.status.State.Phase, originalPhase)
			}
		})
	}
}

// ── updateFn: all alive pods drained → Completed ─────────────────────────────

// TestCompletingState_Execute_UpdateFnAllPodsGone verifies that the updateFn
// returns true and transitions the phase to Completed when all alive pod
// counters are zero. UpdateJobCompleted is also called at this point; since it
// only increments a Prometheus counter it requires no separate assertion.
func TestCompletingState_Execute_UpdateFnAllPodsGone(t *testing.T) {
	tests := []struct {
		name   string
		status vcbatch.JobStatus
	}{
		{
			name:   "all counters zero from start",
			status: vcbatch.JobStatus{Terminating: 0, Pending: 0, Running: 0},
		},
		{
			name: "has Succeeded and Failed but no alive pods",
			status: vcbatch.JobStatus{
				Terminating: 0,
				Pending:     0,
				Running:     0,
				Succeeded:   5,
				Failed:      2,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cap := captureKillJob(t, nil)
			s := &completingState{job: makeJobInfo(vcbatch.Completing)}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			changed := cap.updateFn(&tc.status)

			if !changed {
				t.Error("updateFn should return true when no alive pods remain")
			}
			if tc.status.State.Phase != vcbatch.Completed {
				t.Errorf("phase = %q, want %q", tc.status.State.Phase, vcbatch.Completed)
			}
		})
	}
}
