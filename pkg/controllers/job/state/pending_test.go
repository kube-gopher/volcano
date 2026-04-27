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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"
	"volcano.sh/apis/pkg/apis/bus/v1alpha1"

	"volcano.sh/volcano/pkg/controllers/apis"
)

// --- stub helpers ---

// capturedKillTarget records one KillTarget invocation and preserves the
// updateFn so tests can invoke it to verify state-transition logic.
type capturedKillTarget struct {
	job      *apis.JobInfo
	target   Target
	updateFn UpdateStatusFn
}

func captureKillTarget(t *testing.T, returnErr error) *capturedKillTarget {
	t.Helper()
	original := KillTarget
	c := &capturedKillTarget{}
	KillTarget = func(job *apis.JobInfo, target Target, fn UpdateStatusFn) error {
		c.job = job
		c.target = target
		c.updateFn = fn
		return returnErr
	}
	t.Cleanup(func() { KillTarget = original })
	return c
}

// capturedSyncJob records one SyncJob invocation and preserves the updateFn.
type capturedSyncJob struct {
	job      *apis.JobInfo
	updateFn UpdateStatusFn
}

func captureSyncJob(t *testing.T, returnErr error) *capturedSyncJob {
	t.Helper()
	original := SyncJob
	c := &capturedSyncJob{}
	SyncJob = func(job *apis.JobInfo, fn UpdateStatusFn) error {
		c.job = job
		c.updateFn = fn
		return returnErr
	}
	t.Cleanup(func() { SyncJob = original })
	return c
}

// makeJobInfoWithSpec builds a JobInfo with the given phase and MinAvailable.
func makeJobInfoWithSpec(phase vcbatch.JobPhase, minAvailable int32) *apis.JobInfo {
	return &apis.JobInfo{
		Job: &vcbatch.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "test-job", Namespace: "default"},
			Spec:       vcbatch.JobSpec{MinAvailable: minAvailable},
			Status:     vcbatch.JobStatus{State: vcbatch.JobState{Phase: phase}},
		},
	}
}

// --- RestartJobAction branch ---

// TestPendingState_Execute_RestartJobCallsKillJob verifies RestartJobAction
// delegates to KillJob (not SyncJob or KillTarget).
func TestPendingState_Execute_RestartJobCallsKillJob(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job == nil {
		t.Fatal("KillJob was not called")
	}
}

// TestPendingState_Execute_RestartJobUsesRetainPhaseNone verifies that
// RestartJobAction passes PodRetainPhaseNone — all pods are killed, including
// already-completed ones (unlike Abort/Complete/Terminate which use Soft).
func TestPendingState_Execute_RestartJobUsesRetainPhaseNone(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.podRetainPhase) != 0 {
		t.Errorf("podRetainPhase should be empty (PhaseNone), got %v", c.podRetainPhase)
	}
}

// TestPendingState_Execute_RestartJobUpdateFn verifies the updateFn sets
// Phase to Restarting, increments RetryCount, and returns true.
func TestPendingState_Execute_RestartJobUpdateFn(t *testing.T) {
	tests := []struct {
		name         string
		initialRetry int32
		wantRetry    int32
	}{
		{"from zero", 0, 1},
		{"from non-zero", 2, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			status := &vcbatch.JobStatus{
				RetryCount: tc.initialRetry,
				State:      vcbatch.JobState{Phase: vcbatch.Pending},
			}
			changed := c.updateFn(status)

			if !changed {
				t.Error("updateFn should return true")
			}
			if status.State.Phase != vcbatch.Restarting {
				t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Restarting)
			}
			if status.RetryCount != tc.wantRetry {
				t.Errorf("RetryCount = %d, want %d", status.RetryCount, tc.wantRetry)
			}
		})
	}
}

// TestPendingState_Execute_RestartJobPassesJobInfo verifies the correct
// JobInfo pointer is forwarded to KillJob.
func TestPendingState_Execute_RestartJobPassesJobInfo(t *testing.T) {
	c := captureKillJob(t, nil)
	info := makeJobInfo(vcbatch.Pending)
	s := &pendingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job != info {
		t.Errorf("KillJob received wrong JobInfo: got %p, want %p", c.job, info)
	}
}

// TestPendingState_Execute_RestartJobPropagatesError verifies KillJob errors
// are surfaced.
func TestPendingState_Execute_RestartJobPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if got := s.Execute(Action{Action: v1alpha1.RestartJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

// --- RestartTask / RestartPod / RestartPartition branch ---

// TestPendingState_Execute_RestartTargetActionsCallKillTarget verifies that
// all three granular restart actions delegate to KillTarget.
func TestPendingState_Execute_RestartTargetActionsCallKillTarget(t *testing.T) {
	actions := []struct {
		name   string
		action v1alpha1.Action
	}{
		{"RestartTaskAction", v1alpha1.RestartTaskAction},
		{"RestartPodAction", v1alpha1.RestartPodAction},
		{"RestartPartitionAction", v1alpha1.RestartPartitionAction},
	}
	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillTarget(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if c.job == nil {
				t.Fatal("KillTarget was not called")
			}
		})
	}
}

// TestPendingState_Execute_RestartTargetForwardsTarget verifies that the
// Target embedded in the Action is forwarded unchanged to KillTarget.
func TestPendingState_Execute_RestartTargetForwardsTarget(t *testing.T) {
	tests := []struct {
		name   string
		action v1alpha1.Action
		target Target
	}{
		{
			name:   "task target",
			action: v1alpha1.RestartTaskAction,
			target: Target{TaskName: "worker", Type: TargetTypeTask},
		},
		{
			name:   "pod target",
			action: v1alpha1.RestartPodAction,
			target: Target{TaskName: "worker", PodName: "worker-0", Type: TargetTypePod},
		},
		{
			name:   "partition target",
			action: v1alpha1.RestartPartitionAction,
			target: Target{TaskName: "worker", PodName: "worker-0", PartitionName: "p0", Type: TargetTypePartition},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillTarget(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: tc.action, Target: tc.target}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.target != tc.target {
				t.Errorf("KillTarget received target %+v, want %+v", c.target, tc.target)
			}
		})
	}
}

// TestPendingState_Execute_RestartTargetUpdateFn verifies the updateFn sets
// Phase to Restarting, increments RetryCount, and returns true.
func TestPendingState_Execute_RestartTargetUpdateFn(t *testing.T) {
	c := captureKillTarget(t, nil)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if err := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 1, State: vcbatch.JobState{Phase: vcbatch.Pending}}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Restarting {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Restarting)
	}
	if status.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", status.RetryCount)
	}
}

// TestPendingState_Execute_RestartTargetPropagatesError verifies KillTarget
// errors are surfaced.
func TestPendingState_Execute_RestartTargetPropagatesError(t *testing.T) {
	want := errors.New("kill target failed")
	captureKillTarget(t, want)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if got := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

// --- AbortJobAction / CompleteJobAction / TerminateJobAction branches ---

// TestPendingState_Execute_KillJobBranchesUsesSoftRetainPhase verifies that
// Abort, Complete, and Terminate all call KillJob with PodRetainPhaseSoft.
func TestPendingState_Execute_KillJobBranchesUsesSoftRetainPhase(t *testing.T) {
	actions := []v1alpha1.Action{
		v1alpha1.AbortJobAction,
		v1alpha1.CompleteJobAction,
		v1alpha1.TerminateJobAction,
	}
	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: action}); err != nil {
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
		})
	}
}

// TestPendingState_Execute_KillJobBranchesUpdateFn verifies each action's
// updateFn transitions to the correct target phase and returns true.
func TestPendingState_Execute_KillJobBranchesUpdateFn(t *testing.T) {
	tests := []struct {
		action    v1alpha1.Action
		wantPhase vcbatch.JobPhase
	}{
		{v1alpha1.AbortJobAction, vcbatch.Aborting},
		{v1alpha1.CompleteJobAction, vcbatch.Completing},
		{v1alpha1.TerminateJobAction, vcbatch.Terminating},
	}
	for _, tc := range tests {
		t.Run(string(tc.action), func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			status := &vcbatch.JobStatus{State: vcbatch.JobState{Phase: vcbatch.Pending}}
			changed := c.updateFn(status)

			if !changed {
				t.Error("updateFn should return true")
			}
			if status.State.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", status.State.Phase, tc.wantPhase)
			}
		})
	}
}

// TestPendingState_Execute_KillJobBranchesPropagatesError verifies KillJob
// errors are surfaced for Abort, Complete, and Terminate.
func TestPendingState_Execute_KillJobBranchesPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	actions := []v1alpha1.Action{
		v1alpha1.AbortJobAction,
		v1alpha1.CompleteJobAction,
		v1alpha1.TerminateJobAction,
	}
	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			captureKillJob(t, want)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if got := s.Execute(Action{Action: action}); !errors.Is(got, want) {
				t.Errorf("Execute returned %v, want %v", got, want)
			}
		})
	}
}

// --- default branch (SyncJob) ---

// TestPendingState_Execute_DefaultCallsSyncJob verifies the default path
// calls SyncJob (not KillJob or KillTarget).
func TestPendingState_Execute_DefaultCallsSyncJob(t *testing.T) {
	c := captureSyncJob(t, nil)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job == nil {
		t.Fatal("SyncJob was not called")
	}
}

// TestPendingState_Execute_DefaultPassesJobInfo verifies the correct JobInfo
// pointer is forwarded to SyncJob.
func TestPendingState_Execute_DefaultPassesJobInfo(t *testing.T) {
	c := captureSyncJob(t, nil)
	info := makeJobInfoWithSpec(vcbatch.Pending, 1)
	s := &pendingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job != info {
		t.Errorf("SyncJob received wrong JobInfo: got %p, want %p", c.job, info)
	}
}

// TestPendingState_Execute_DefaultUpdateFnTransitionsToRunning verifies the
// updateFn transitions to Running when Running+Succeeded+Failed >= MinAvailable.
func TestPendingState_Execute_DefaultUpdateFnTransitionsToRunning(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		status       vcbatch.JobStatus
	}{
		{
			name:         "sum equals MinAvailable",
			minAvailable: 3,
			status:       vcbatch.JobStatus{Running: 3},
		},
		{
			name:         "sum exceeds MinAvailable",
			minAvailable: 2,
			status:       vcbatch.JobStatus{Running: 1, Succeeded: 1, Failed: 1},
		},
		{
			name:         "only Succeeded meets MinAvailable",
			minAvailable: 2,
			status:       vcbatch.JobStatus{Succeeded: 2},
		},
		{
			name:         "only Failed meets MinAvailable",
			minAvailable: 1,
			status:       vcbatch.JobStatus{Failed: 1},
		},
		{
			name:         "MinAvailable is zero",
			minAvailable: 0,
			status:       vcbatch.JobStatus{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureSyncJob(t, nil)
			s := &pendingState{job: makeJobInfoWithSpec(vcbatch.Pending, tc.minAvailable)}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			changed := c.updateFn(&tc.status)

			if !changed {
				t.Error("updateFn should return true when MinAvailable is met")
			}
			if tc.status.State.Phase != vcbatch.Running {
				t.Errorf("phase = %q, want %q", tc.status.State.Phase, vcbatch.Running)
			}
		})
	}
}

// TestPendingState_Execute_DefaultUpdateFnStaysPending verifies the updateFn
// returns false and leaves phase unchanged when Running+Succeeded+Failed <
// MinAvailable.
func TestPendingState_Execute_DefaultUpdateFnStaysPending(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		status       vcbatch.JobStatus
	}{
		{
			name:         "no pods at all",
			minAvailable: 1,
			status:       vcbatch.JobStatus{},
		},
		{
			name:         "sum one short of MinAvailable",
			minAvailable: 3,
			status:       vcbatch.JobStatus{Running: 1, Succeeded: 1},
		},
		{
			name:         "only Pending pods, not counted",
			minAvailable: 2,
			status:       vcbatch.JobStatus{Pending: 5, Running: 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureSyncJob(t, nil)
			s := &pendingState{job: makeJobInfoWithSpec(vcbatch.Pending, tc.minAvailable)}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			originalPhase := tc.status.State.Phase
			changed := c.updateFn(&tc.status)

			if changed {
				t.Error("updateFn should return false when MinAvailable is not met")
			}
			if tc.status.State.Phase != originalPhase {
				t.Errorf("phase should not change: got %q, want %q", tc.status.State.Phase, originalPhase)
			}
		})
	}
}

// TestPendingState_Execute_DefaultPropagatesError verifies SyncJob errors are
// surfaced for the default branch.
func TestPendingState_Execute_DefaultPropagatesError(t *testing.T) {
	want := errors.New("sync failed")
	captureSyncJob(t, want)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}
