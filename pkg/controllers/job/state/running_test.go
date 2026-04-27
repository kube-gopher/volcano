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

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"
	"volcano.sh/apis/pkg/apis/bus/v1alpha1"

	"volcano.sh/volcano/pkg/controllers/apis"
)

// makeRunningJobInfo builds a JobInfo tuned for running-state default-branch
// tests. minSuccess may be nil to leave it unset.
func makeRunningJobInfo(minAvailable int32, minSuccess *int32, tasks []vcbatch.TaskSpec) *apis.JobInfo {
	return &apis.JobInfo{
		Job: &vcbatch.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "test-job", Namespace: "default"},
			Spec: vcbatch.JobSpec{
				MinAvailable: minAvailable,
				MinSuccess:   minSuccess,
				Tasks:        tasks,
			},
			Status: vcbatch.JobStatus{
				State: vcbatch.JobState{Phase: vcbatch.Running},
			},
		},
	}
}

// taskStatus is a convenience constructor for TaskStatusCount entries.
func taskStatus(succeeded int32) vcbatch.TaskState {
	return vcbatch.TaskState{
		Phase: map[v1.PodPhase]int32{v1.PodSucceeded: succeeded},
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// RestartJobAction branch
// ══════════════════════════════════════════════════════════════════════════════

func TestRunningState_Execute_RestartJobCallsKillJob(t *testing.T) {
	cap := captureKillJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.job == nil {
		t.Fatal("KillJob was not called")
	}
}

func TestRunningState_Execute_RestartJobUsesRetainPhaseNone(t *testing.T) {
	cap := captureKillJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cap.podRetainPhase) != 0 {
		t.Errorf("podRetainPhase should be empty (PhaseNone), got %v", cap.podRetainPhase)
	}
}

func TestRunningState_Execute_RestartJobUpdateFn(t *testing.T) {
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
			cap := captureKillJob(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cap.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			status := &vcbatch.JobStatus{
				RetryCount: tc.initialRetry,
				State:      vcbatch.JobState{Phase: vcbatch.Running},
			}
			changed := cap.updateFn(status)

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

func TestRunningState_Execute_RestartJobPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if got := s.Execute(Action{Action: v1alpha1.RestartJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// RestartTask / RestartPod / RestartPartition branch
// ══════════════════════════════════════════════════════════════════════════════

func TestRunningState_Execute_RestartTargetActionsCallKillTarget(t *testing.T) {
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
			cap := captureKillTarget(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cap.job == nil {
				t.Fatal("KillTarget was not called")
			}
		})
	}
}

func TestRunningState_Execute_RestartTargetForwardsTarget(t *testing.T) {
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
			cap := captureKillTarget(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if err := s.Execute(Action{Action: tc.action, Target: tc.target}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cap.target != tc.target {
				t.Errorf("KillTarget received target %+v, want %+v", cap.target, tc.target)
			}
		})
	}
}

func TestRunningState_Execute_RestartTargetUpdateFn(t *testing.T) {
	cap := captureKillTarget(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if err := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 1, State: vcbatch.JobState{Phase: vcbatch.Running}}
	changed := cap.updateFn(status)

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

func TestRunningState_Execute_RestartTargetPropagatesError(t *testing.T) {
	want := errors.New("kill target failed")
	captureKillTarget(t, want)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if got := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// AbortJobAction / TerminateJobAction / CompleteJobAction branches
// ══════════════════════════════════════════════════════════════════════════════

func TestRunningState_Execute_KillJobBranchesUsesSoftRetainPhase(t *testing.T) {
	for _, action := range []v1alpha1.Action{
		v1alpha1.AbortJobAction,
		v1alpha1.TerminateJobAction,
		v1alpha1.CompleteJobAction,
	} {
		t.Run(string(action), func(t *testing.T) {
			cap := captureKillJob(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if err := s.Execute(Action{Action: action}); err != nil {
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
		})
	}
}

func TestRunningState_Execute_KillJobBranchesUpdateFn(t *testing.T) {
	tests := []struct {
		action    v1alpha1.Action
		wantPhase vcbatch.JobPhase
	}{
		{v1alpha1.AbortJobAction, vcbatch.Aborting},
		{v1alpha1.TerminateJobAction, vcbatch.Terminating},
		{v1alpha1.CompleteJobAction, vcbatch.Completing},
	}
	for _, tc := range tests {
		t.Run(string(tc.action), func(t *testing.T) {
			cap := captureKillJob(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cap.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			status := &vcbatch.JobStatus{State: vcbatch.JobState{Phase: vcbatch.Running}}
			changed := cap.updateFn(status)

			if !changed {
				t.Error("updateFn should return true")
			}
			if status.State.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", status.State.Phase, tc.wantPhase)
			}
		})
	}
}

func TestRunningState_Execute_KillJobBranchesPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	for _, action := range []v1alpha1.Action{
		v1alpha1.AbortJobAction,
		v1alpha1.TerminateJobAction,
		v1alpha1.CompleteJobAction,
	} {
		t.Run(string(action), func(t *testing.T) {
			captureKillJob(t, want)
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if got := s.Execute(Action{Action: action}); !errors.Is(got, want) {
				t.Errorf("Execute returned %v, want %v", got, want)
			}
		})
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// default branch (SyncJob) — updateFn logic
// ══════════════════════════════════════════════════════════════════════════════

func TestRunningState_Execute_DefaultCallsSyncJob(t *testing.T) {
	cap := captureSyncJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.job == nil {
		t.Fatal("SyncJob was not called")
	}
}

func TestRunningState_Execute_DefaultPropagatesError(t *testing.T) {
	want := errors.New("sync failed")
	captureSyncJob(t, want)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

// TestRunningState_Execute_DefaultUpdateFnScaleToZero verifies that when
// total replicas is zero (scale-to-zero) the updateFn returns false and leaves
// the phase unchanged.
func TestRunningState_Execute_DefaultUpdateFnScaleToZero(t *testing.T) {
	cap := captureSyncJob(t, nil)
	s := &runningState{job: makeRunningJobInfo(0, nil, []vcbatch.TaskSpec{{Replicas: 0}})}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := &vcbatch.JobStatus{State: vcbatch.JobState{Phase: vcbatch.Running}}
	changed := cap.updateFn(status)

	if changed {
		t.Error("updateFn should return false for scale-to-zero")
	}
	if status.State.Phase != vcbatch.Running {
		t.Errorf("phase should stay Running, got %q", status.State.Phase)
	}
}

// TestRunningState_Execute_DefaultUpdateFnMinSuccessEarlyExit verifies that
// when Succeeded >= MinSuccess the job completes before checking all-terminal.
func TestRunningState_Execute_DefaultUpdateFnMinSuccessEarlyExit(t *testing.T) {
	tests := []struct {
		name      string
		minSuc    int32
		succeeded int32
	}{
		{"exactly meets minSuccess", 3, 3},
		{"exceeds minSuccess", 3, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cap := captureSyncJob(t, nil)
			s := &runningState{job: makeRunningJobInfo(1, int32Ptr(tc.minSuc),
				[]vcbatch.TaskSpec{{Replicas: 5}})}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			status := &vcbatch.JobStatus{Succeeded: tc.succeeded, Running: 2}
			changed := cap.updateFn(status)

			if !changed {
				t.Error("updateFn should return true")
			}
			if status.State.Phase != vcbatch.Completed {
				t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Completed)
			}
		})
	}
}

// TestRunningState_Execute_DefaultUpdateFnPerTaskCheckFails verifies that when
// all pods are terminal and a task's per-task MinAvailable is not met the job
// transitions to Failed.
func TestRunningState_Execute_DefaultUpdateFnPerTaskCheckFails(t *testing.T) {
	// MinAvailable(3) >= totalTaskMinAvailable(2) → per-task loop runs.
	// Task "worker" needs 2 succeeded but only 1 succeeded → Failed.
	cap := captureSyncJob(t, nil)
	s := &runningState{job: makeRunningJobInfo(3, nil, []vcbatch.TaskSpec{
		{Name: "worker", Replicas: 3, MinAvailable: int32Ptr(2)},
	})}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := &vcbatch.JobStatus{
		Succeeded: 1,
		Failed:    2, // Succeeded+Failed == jobReplicas (3)
		TaskStatusCount: map[string]vcbatch.TaskState{
			"worker": taskStatus(1), // only 1 succeeded, need 2
		},
	}
	changed := cap.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Failed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Failed)
	}
}

// TestRunningState_Execute_DefaultUpdateFnPerTaskCheckSkipped verifies that
// the per-task loop is skipped when MinAvailable < totalTaskMinAvailable.
func TestRunningState_Execute_DefaultUpdateFnPerTaskCheckSkipped(t *testing.T) {
	// MinAvailable(1) < totalTaskMinAvailable(3) → loop skipped → use 3b.
	// Succeeded(3) >= MinAvailable(1) → Completed.
	cap := captureSyncJob(t, nil)
	s := &runningState{job: makeRunningJobInfo(1, nil, []vcbatch.TaskSpec{
		{Name: "worker", Replicas: 3, MinAvailable: int32Ptr(3)},
	})}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := &vcbatch.JobStatus{
		Succeeded: 3,
		Failed:    0, // all terminal (3+0=3=jobReplicas)
		TaskStatusCount: map[string]vcbatch.TaskState{
			"worker": taskStatus(3),
		},
	}
	changed := cap.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Completed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Completed)
	}
}

// TestRunningState_Execute_DefaultUpdateFnPerTaskNoStatus verifies that a task
// with MinAvailable set but absent from TaskStatusCount does not block
// completion (ok=false skips the inner check).
func TestRunningState_Execute_DefaultUpdateFnPerTaskNoStatus(t *testing.T) {
	// MinAvailable(2) >= totalTaskMinAvailable(2) → loop runs.
	// "worker" has MinAvailable but has NO entry in TaskStatusCount → ok=false,
	// loop continues without failing → falls through to 3b → Completed.
	cap := captureSyncJob(t, nil)
	s := &runningState{job: makeRunningJobInfo(2, nil, []vcbatch.TaskSpec{
		{Name: "worker", Replicas: 2, MinAvailable: int32Ptr(2)},
	})}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := &vcbatch.JobStatus{
		Succeeded:       2,
		Failed:          0, // all terminal
		TaskStatusCount: map[string]vcbatch.TaskState{},
	}
	changed := cap.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Completed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Completed)
	}
}

// TestRunningState_Execute_DefaultUpdateFnAllTerminalOutcomes verifies the
// 3b sub-branch that runs after per-task checks pass (or are skipped).
func TestRunningState_Execute_DefaultUpdateFnAllTerminalOutcomes(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		minSuccess   *int32
		status       vcbatch.JobStatus
		wantPhase    vcbatch.JobPhase
	}{
		{
			// minSuccess set, Succeeded < minSuccess → Failed
			name:         "minSuccess not met → Failed",
			minAvailable: 1,
			minSuccess:   int32Ptr(4),
			// jobReplicas=3, all terminal, Succeeded=2 < minSuccess=4
			status:    vcbatch.JobStatus{Succeeded: 2, Failed: 1},
			wantPhase: vcbatch.Failed,
		},
		{
			// no minSuccess, Succeeded >= MinAvailable → Completed
			name:         "Succeeded meets MinAvailable → Completed",
			minAvailable: 2,
			minSuccess:   nil,
			// jobReplicas=3, all terminal, Succeeded=2 >= MinAvailable=2
			status:    vcbatch.JobStatus{Succeeded: 2, Failed: 1},
			wantPhase: vcbatch.Completed,
		},
		{
			// no minSuccess, Succeeded < MinAvailable → Failed
			name:         "Succeeded below MinAvailable → Failed",
			minAvailable: 3,
			minSuccess:   nil,
			// jobReplicas=3, all terminal, Succeeded=1 < MinAvailable=3
			status:    vcbatch.JobStatus{Succeeded: 1, Failed: 2},
			wantPhase: vcbatch.Failed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cap := captureSyncJob(t, nil)
			// No tasks with per-task MinAvailable so the loop is a no-op.
			s := &runningState{job: makeRunningJobInfo(tc.minAvailable, tc.minSuccess,
				[]vcbatch.TaskSpec{{Replicas: 3}})}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			changed := cap.updateFn(&tc.status)

			if !changed {
				t.Error("updateFn should return true when all pods are terminal")
			}
			if tc.status.State.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", tc.status.State.Phase, tc.wantPhase)
			}
		})
	}
}

// TestRunningState_Execute_DefaultUpdateFnTooManyPending verifies that when
// too many pods are Pending the job falls back to Pending phase.
func TestRunningState_Execute_DefaultUpdateFnTooManyPending(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		taskReplicas int32
		pending      int32
	}{
		{
			// jobReplicas=4, MinAvailable=2, threshold=4-2=2; Pending=3 > 2 → Pending
			name:         "pending exceeds threshold",
			minAvailable: 2,
			taskReplicas: 4,
			pending:      3,
		},
		{
			// jobReplicas=5, MinAvailable=1, threshold=5-1=4; Pending=5 > 4 → Pending
			name:         "all pods pending",
			minAvailable: 1,
			taskReplicas: 5,
			pending:      5,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cap := captureSyncJob(t, nil)
			s := &runningState{job: makeRunningJobInfo(tc.minAvailable, nil,
				[]vcbatch.TaskSpec{{Replicas: tc.taskReplicas}})}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			status := &vcbatch.JobStatus{Pending: tc.pending}
			changed := cap.updateFn(status)

			if !changed {
				t.Error("updateFn should return true")
			}
			if status.State.Phase != vcbatch.Pending {
				t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Pending)
			}
		})
	}
}

// TestRunningState_Execute_DefaultUpdateFnKeepsRunning verifies that when no
// terminal condition is met the updateFn returns false and stays Running.
func TestRunningState_Execute_DefaultUpdateFnKeepsRunning(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		taskReplicas int32
		status       vcbatch.JobStatus
	}{
		{
			// Some pods running, no terminal condition triggered.
			name:         "pods still running",
			minAvailable: 2,
			taskReplicas: 4,
			status:       vcbatch.JobStatus{Running: 2, Pending: 2},
		},
		{
			// Pending exactly at threshold (not strictly greater).
			name:         "pending at threshold boundary",
			minAvailable: 2,
			taskReplicas: 4,
			// threshold = 4-2=2; Pending=2 is NOT > 2 → no Pending transition
			status: vcbatch.JobStatus{Pending: 2, Running: 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cap := captureSyncJob(t, nil)
			s := &runningState{job: makeRunningJobInfo(tc.minAvailable, nil,
				[]vcbatch.TaskSpec{{Replicas: tc.taskReplicas}})}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			status := tc.status
			changed := cap.updateFn(&status)

			if changed {
				t.Error("updateFn should return false when no terminal condition met")
			}
			if status.State.Phase != vcbatch.Running && status.State.Phase != "" {
				t.Errorf("phase should remain unchanged, got %q", status.State.Phase)
			}
		})
	}
}
