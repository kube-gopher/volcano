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

// --- ResumeJobAction branch ---

// TestAbortingState_Execute_ResumeCallsKillJob verifies that ResumeJobAction
// delegates to KillJob.
func TestAbortingState_Execute_ResumeCallsKillJob(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

	if err := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job == nil {
		t.Fatal("KillJob was not called")
	}
}

// TestAbortingState_Execute_ResumeUsesSoftRetainPhase verifies that
// ResumeJobAction passes PodRetainPhaseSoft to KillJob.
func TestAbortingState_Execute_ResumeUsesSoftRetainPhase(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

	if err := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for phase := range PodRetainPhaseSoft {
		if _, ok := c.podRetainPhase[phase]; !ok {
			t.Errorf("podRetainPhase missing %q", phase)
		}
	}
	if len(c.podRetainPhase) != len(PodRetainPhaseSoft) {
		t.Errorf("podRetainPhase has %d entries, want %d", len(c.podRetainPhase), len(PodRetainPhaseSoft))
	}
}

// TestAbortingState_Execute_ResumeUpdateFnSetsRestarting verifies the
// updateFn transitions the phase to Restarting and returns true.
func TestAbortingState_Execute_ResumeUpdateFnSetsRestarting(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

	if err := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn == nil {
		t.Fatal("expected non-nil updateFn for ResumeJobAction")
	}

	status := &vcbatch.JobStatus{State: vcbatch.JobState{Phase: vcbatch.Aborting}}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Restarting {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Restarting)
	}
}

// TestAbortingState_Execute_ResumeUpdateFnIncrementsRetryCount verifies the
// updateFn increments RetryCount on each Resume.
func TestAbortingState_Execute_ResumeUpdateFnIncrementsRetryCount(t *testing.T) {
	tests := []struct {
		name         string
		initialRetry int32
		wantRetry    int32
	}{
		{"from zero", 0, 1},
		{"from non-zero", 5, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

			if err := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			status := &vcbatch.JobStatus{RetryCount: tc.initialRetry}
			c.updateFn(status)

			if status.RetryCount != tc.wantRetry {
				t.Errorf("RetryCount = %d, want %d", status.RetryCount, tc.wantRetry)
			}
		})
	}
}

// TestAbortingState_Execute_ResumePassesJobInfo verifies the correct JobInfo
// pointer is forwarded to KillJob for ResumeJobAction.
func TestAbortingState_Execute_ResumePassesJobInfo(t *testing.T) {
	c := captureKillJob(t, nil)
	info := makeJobInfo(vcbatch.Aborting)
	s := &abortingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job != info {
		t.Errorf("KillJob received wrong JobInfo: got %p, want %p", c.job, info)
	}
}

// TestAbortingState_Execute_ResumePropagatesError verifies that KillJob errors
// are surfaced for ResumeJobAction.
func TestAbortingState_Execute_ResumePropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

	if got := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

// --- default branch (all non-Resume actions) ---

// TestAbortingState_Execute_DefaultActionsCallKillJob verifies that every
// non-Resume action delegates to KillJob.
func TestAbortingState_Execute_DefaultActionsCallKillJob(t *testing.T) {
	defaultActions := []struct {
		name   string
		action v1alpha1.Action
	}{
		{"SyncJobAction", v1alpha1.SyncJobAction},
		{"RestartJobAction", v1alpha1.RestartJobAction},
		{"AbortJobAction", v1alpha1.AbortJobAction},
		{"TerminateJobAction", v1alpha1.TerminateJobAction},
		{"CompleteJobAction", v1alpha1.CompleteJobAction},
		{"RestartTaskAction", v1alpha1.RestartTaskAction},
		{"RestartPodAction", v1alpha1.RestartPodAction},
		{"RestartPartitionAction", v1alpha1.RestartPartitionAction},
		{"UnknownAction", v1alpha1.Action("Unknown")},
	}

	for _, tc := range defaultActions {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if c.job == nil {
				t.Fatal("KillJob was not called")
			}
		})
	}
}

// TestAbortingState_Execute_DefaultUsesSoftRetainPhase verifies the default
// branch also passes PodRetainPhaseSoft to KillJob.
func TestAbortingState_Execute_DefaultUsesSoftRetainPhase(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for phase := range PodRetainPhaseSoft {
		if _, ok := c.podRetainPhase[phase]; !ok {
			t.Errorf("podRetainPhase missing %q", phase)
		}
	}
	if len(c.podRetainPhase) != len(PodRetainPhaseSoft) {
		t.Errorf("podRetainPhase has %d entries, want %d", len(c.podRetainPhase), len(PodRetainPhaseSoft))
	}
}

// TestAbortingState_Execute_DefaultNonNilUpdateFn verifies the default branch
// passes a non-nil updateFn (unlike abortedState which passes nil).
func TestAbortingState_Execute_DefaultNonNilUpdateFn(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn == nil {
		t.Error("default branch updateFn must not be nil for abortingState")
	}
}

// TestAbortingState_Execute_DefaultUpdateFnAlivePods verifies that the default
// updateFn returns false and keeps the phase unchanged while any "alive" pod
// counter (Terminating, Pending, or Running) is non-zero.
func TestAbortingState_Execute_DefaultUpdateFnAlivePods(t *testing.T) {
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
			c := captureKillJob(t, nil)
			s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			originalPhase := tc.status.State.Phase
			changed := c.updateFn(&tc.status)

			if changed {
				t.Error("updateFn should return false while alive pods exist")
			}
			if tc.status.State.Phase != originalPhase {
				t.Errorf("phase should not change: got %q, want %q", tc.status.State.Phase, originalPhase)
			}
		})
	}
}

// TestAbortingState_Execute_DefaultUpdateFnAllPodsGone verifies that the
// default updateFn returns true and transitions the phase to Aborted when all
// alive pod counters are zero.
func TestAbortingState_Execute_DefaultUpdateFnAllPodsGone(t *testing.T) {
	tests := []struct {
		name   string
		status vcbatch.JobStatus
	}{
		{
			name:   "all counters zero",
			status: vcbatch.JobStatus{Terminating: 0, Pending: 0, Running: 0},
		},
		{
			name: "has Succeeded and Failed but no alive pods",
			status: vcbatch.JobStatus{
				Terminating: 0,
				Pending:     0,
				Running:     0,
				Succeeded:   3,
				Failed:      1,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			changed := c.updateFn(&tc.status)

			if !changed {
				t.Error("updateFn should return true when no alive pods remain")
			}
			if tc.status.State.Phase != vcbatch.Aborted {
				t.Errorf("phase = %q, want %q", tc.status.State.Phase, vcbatch.Aborted)
			}
		})
	}
}

// TestAbortingState_Execute_DefaultPropagatesError verifies that KillJob
// errors are surfaced for default-branch actions.
func TestAbortingState_Execute_DefaultPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &abortingState{job: makeJobInfo(vcbatch.Aborting)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}
