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

// makeRestartingJobInfo builds a JobInfo configured for restarting-state tests.
// taskReplicas controls how many replicas each task has; maxRetry sets the
// job-level retry ceiling.
func makeRestartingJobInfo(maxRetry int32, taskReplicas ...int32) *apis.JobInfo {
	tasks := make([]vcbatch.TaskSpec, len(taskReplicas))
	for i, r := range taskReplicas {
		tasks[i] = vcbatch.TaskSpec{Replicas: r}
	}
	return &apis.JobInfo{
		Job: &vcbatch.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "test-job", Namespace: "default"},
			Spec: vcbatch.JobSpec{
				MaxRetry: maxRetry,
				Tasks:    tasks,
			},
			Status: vcbatch.JobStatus{
				State: vcbatch.JobState{Phase: vcbatch.Restarting},
			},
		},
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// restartingUpdateStatus — direct method tests
// ══════════════════════════════════════════════════════════════════════════════

// TestRestartingState_UpdateStatus_ExceedsMaxRetry verifies that when
// RetryCount >= MaxRetry the method sets Phase to Failed and returns true.
func TestRestartingState_UpdateStatus_ExceedsMaxRetry(t *testing.T) {
	tests := []struct {
		name       string
		maxRetry   int32
		retryCount int32
	}{
		{"equal to maxRetry", 3, 3},
		{"greater than maxRetry", 3, 5},
		{"maxRetry is zero", 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &restartingState{job: makeRestartingJobInfo(tc.maxRetry, 3)}
			status := &vcbatch.JobStatus{
				RetryCount: tc.retryCount,
				State:      vcbatch.JobState{Phase: vcbatch.Restarting},
			}

			changed := s.restartingUpdateStatus(status)

			if !changed {
				t.Error("expected true when RetryCount >= MaxRetry")
			}
			if status.State.Phase != vcbatch.Failed {
				t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Failed)
			}
		})
	}
}

// TestRestartingState_UpdateStatus_MeetsMinAvailable verifies that when
// RetryCount < MaxRetry and (total replicas - Terminating) >= status.MinAvailable
// the method sets Phase to Pending and returns true.
func TestRestartingState_UpdateStatus_MeetsMinAvailable(t *testing.T) {
	tests := []struct {
		name         string
		taskReplicas []int32 // total is their sum
		terminating  int32
		minAvailable int32
	}{
		{
			name:         "no terminating pods, exactly meets minAvailable",
			taskReplicas: []int32{3, 2}, // total = 5
			terminating:  0,
			minAvailable: 5, // 5-0=5 >= 5
		},
		{
			name:         "some terminating pods, still meets minAvailable",
			taskReplicas: []int32{3, 2}, // total = 5
			terminating:  2,
			minAvailable: 2, // 5-2=3 >= 2
		},
		{
			name:         "single task, minAvailable is zero",
			taskReplicas: []int32{3},
			terminating:  3,
			minAvailable: 0, // 3-3=0 >= 0
		},
		{
			name:         "total exceeds minAvailable",
			taskReplicas: []int32{5},
			terminating:  1,
			minAvailable: 2, // 5-1=4 >= 2
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &restartingState{job: makeRestartingJobInfo(10, tc.taskReplicas...)}
			status := &vcbatch.JobStatus{
				RetryCount:   1, // < MaxRetry (10)
				Terminating:  tc.terminating,
				MinAvailable: tc.minAvailable,
				State:        vcbatch.JobState{Phase: vcbatch.Restarting},
			}

			changed := s.restartingUpdateStatus(status)

			if !changed {
				t.Error("expected true when minAvailable condition is met")
			}
			if status.State.Phase != vcbatch.Pending {
				t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Pending)
			}
		})
	}
}

// TestRestartingState_UpdateStatus_StillWaiting verifies that when
// RetryCount < MaxRetry and (total - Terminating) < status.MinAvailable the
// method returns false and leaves the phase unchanged.
func TestRestartingState_UpdateStatus_StillWaiting(t *testing.T) {
	tests := []struct {
		name         string
		taskReplicas []int32
		terminating  int32
		minAvailable int32
	}{
		{
			name:         "one pod short after terminating subtracted",
			taskReplicas: []int32{3}, // total = 3
			terminating:  1,
			minAvailable: 3, // 3-1=2 < 3
		},
		{
			name:         "all pods are terminating",
			taskReplicas: []int32{4}, // total = 4
			terminating:  4,
			minAvailable: 1, // 4-4=0 < 1
		},
		{
			name:         "no tasks at all",
			taskReplicas: []int32{}, // total = 0
			terminating:  0,
			minAvailable: 1, // 0-0=0 < 1
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &restartingState{job: makeRestartingJobInfo(10, tc.taskReplicas...)}
			status := &vcbatch.JobStatus{
				RetryCount:   1, // < MaxRetry (10)
				Terminating:  tc.terminating,
				MinAvailable: tc.minAvailable,
				State:        vcbatch.JobState{Phase: vcbatch.Restarting},
			}
			originalPhase := status.State.Phase

			changed := s.restartingUpdateStatus(status)

			if changed {
				t.Error("expected false while waiting for pods to terminate")
			}
			if status.State.Phase != originalPhase {
				t.Errorf("phase should not change: got %q, want %q", status.State.Phase, originalPhase)
			}
		})
	}
}

// TestRestartingState_UpdateStatus_MaxRetryTakesPrecedence verifies that the
// RetryCount >= MaxRetry check runs before the minAvailable check — even when
// the minAvailable condition would also be satisfied.
func TestRestartingState_UpdateStatus_MaxRetryTakesPrecedence(t *testing.T) {
	s := &restartingState{job: makeRestartingJobInfo(3, 5)}
	status := &vcbatch.JobStatus{
		RetryCount:   3, // == MaxRetry → should trigger Failed
		Terminating:  0,
		MinAvailable: 1, // 5-0=5 >= 1, would satisfy Pending — but Failed wins
		State:        vcbatch.JobState{Phase: vcbatch.Restarting},
	}

	changed := s.restartingUpdateStatus(status)

	if !changed {
		t.Error("expected true")
	}
	if status.State.Phase != vcbatch.Failed {
		t.Errorf("phase = %q, want %q (MaxRetry check must take precedence)", status.State.Phase, vcbatch.Failed)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// Execute — SyncJobAction branch
// ══════════════════════════════════════════════════════════════════════════════

// TestRestartingState_Execute_SyncJobCallsSyncJob verifies that SyncJobAction
// delegates to SyncJob.
func TestRestartingState_Execute_SyncJobCallsSyncJob(t *testing.T) {
	cap := captureSyncJob(t, nil)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.job == nil {
		t.Fatal("SyncJob was not called")
	}
}

// TestRestartingState_Execute_SyncJobPassesJobInfo verifies the correct
// JobInfo pointer is forwarded to SyncJob.
func TestRestartingState_Execute_SyncJobPassesJobInfo(t *testing.T) {
	cap := captureSyncJob(t, nil)
	info := makeRestartingJobInfo(5, 3)
	s := &restartingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.job != info {
		t.Errorf("SyncJob received wrong JobInfo: got %p, want %p", cap.job, info)
	}
}

// TestRestartingState_Execute_SyncJobUpdateFnIsRestartingUpdateStatus verifies
// that the updateFn passed to SyncJob is restartingUpdateStatus by calling it
// and observing its output.
func TestRestartingState_Execute_SyncJobUpdateFnIsRestartingUpdateStatus(t *testing.T) {
	cap := captureSyncJob(t, nil)
	// MaxRetry=2, RetryCount=2 → updateFn should set Failed
	s := &restartingState{job: makeRestartingJobInfo(2, 3)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 2}
	changed := cap.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Failed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Failed)
	}
}

// TestRestartingState_Execute_SyncJobPropagatesError verifies SyncJob errors
// are surfaced.
func TestRestartingState_Execute_SyncJobPropagatesError(t *testing.T) {
	want := errors.New("sync failed")
	captureSyncJob(t, want)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// Execute — RestartTask / RestartPod / RestartPartition branch
// ══════════════════════════════════════════════════════════════════════════════

// TestRestartingState_Execute_RestartTargetActionsCallKillTarget verifies that
// all three granular restart actions delegate to KillTarget.
func TestRestartingState_Execute_RestartTargetActionsCallKillTarget(t *testing.T) {
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
			s := &restartingState{job: makeRestartingJobInfo(5, 3)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if cap.job == nil {
				t.Fatal("KillTarget was not called")
			}
		})
	}
}

// TestRestartingState_Execute_RestartTargetForwardsTarget verifies the Target
// embedded in the Action is passed unchanged to KillTarget.
func TestRestartingState_Execute_RestartTargetForwardsTarget(t *testing.T) {
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
			s := &restartingState{job: makeRestartingJobInfo(5, 3)}

			if err := s.Execute(Action{Action: tc.action, Target: tc.target}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cap.target != tc.target {
				t.Errorf("KillTarget received target %+v, want %+v", cap.target, tc.target)
			}
		})
	}
}

// TestRestartingState_Execute_RestartTargetUpdateFnIsRestartingUpdateStatus
// verifies the updateFn forwarded to KillTarget is restartingUpdateStatus.
func TestRestartingState_Execute_RestartTargetUpdateFnIsRestartingUpdateStatus(t *testing.T) {
	cap := captureKillTarget(t, nil)
	// MaxRetry=2, RetryCount=2 → updateFn should set Failed
	s := &restartingState{job: makeRestartingJobInfo(2, 3)}

	if err := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 2}
	changed := cap.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Failed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Failed)
	}
}

// TestRestartingState_Execute_RestartTargetPropagatesError verifies KillTarget
// errors are surfaced.
func TestRestartingState_Execute_RestartTargetPropagatesError(t *testing.T) {
	want := errors.New("kill target failed")
	captureKillTarget(t, want)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if got := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// Execute — default branch (all other actions → KillJob)
// ══════════════════════════════════════════════════════════════════════════════

// TestRestartingState_Execute_DefaultActionsCallKillJob verifies that every
// action not handled explicitly delegates to KillJob.
func TestRestartingState_Execute_DefaultActionsCallKillJob(t *testing.T) {
	defaultActions := []struct {
		name   string
		action v1alpha1.Action
	}{
		{"RestartJobAction", v1alpha1.RestartJobAction},
		{"AbortJobAction", v1alpha1.AbortJobAction},
		{"TerminateJobAction", v1alpha1.TerminateJobAction},
		{"CompleteJobAction", v1alpha1.CompleteJobAction},
		{"ResumeJobAction", v1alpha1.ResumeJobAction},
		{"UnknownAction", v1alpha1.Action("Unknown")},
	}
	for _, tc := range defaultActions {
		t.Run(tc.name, func(t *testing.T) {
			cap := captureKillJob(t, nil)
			s := &restartingState{job: makeRestartingJobInfo(5, 3)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if cap.job == nil {
				t.Fatal("KillJob was not called")
			}
		})
	}
}

// TestRestartingState_Execute_DefaultUsesRetainPhaseNone verifies the default
// branch passes PodRetainPhaseNone — all pods are killed including completed
// ones, to allow a clean restart.
func TestRestartingState_Execute_DefaultUsesRetainPhaseNone(t *testing.T) {
	cap := captureKillJob(t, nil)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cap.podRetainPhase) != 0 {
		t.Errorf("podRetainPhase should be empty (PhaseNone), got %v", cap.podRetainPhase)
	}
}

// TestRestartingState_Execute_DefaultPassesJobInfo verifies the correct
// JobInfo pointer is forwarded to KillJob.
func TestRestartingState_Execute_DefaultPassesJobInfo(t *testing.T) {
	cap := captureKillJob(t, nil)
	info := makeRestartingJobInfo(5, 3)
	s := &restartingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.job != info {
		t.Errorf("KillJob received wrong JobInfo: got %p, want %p", cap.job, info)
	}
}

// TestRestartingState_Execute_DefaultUpdateFnIsRestartingUpdateStatus verifies
// the updateFn passed to KillJob is restartingUpdateStatus.
func TestRestartingState_Execute_DefaultUpdateFnIsRestartingUpdateStatus(t *testing.T) {
	cap := captureKillJob(t, nil)
	// MaxRetry=2, RetryCount=2 → updateFn should set Failed
	s := &restartingState{job: makeRestartingJobInfo(2, 3)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 2}
	changed := cap.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Failed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Failed)
	}
}

// TestRestartingState_Execute_DefaultPropagatesError verifies KillJob errors
// are surfaced for the default branch.
func TestRestartingState_Execute_DefaultPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if got := s.Execute(Action{Action: v1alpha1.RestartJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}
