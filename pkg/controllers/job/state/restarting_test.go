/*
Copyright 2017 The Volcano Authors.

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

	"volcano.sh/volcano/pkg/controllers/apis"
)

func makeRestartingJobInfo(maxRetry int32, taskReplicas ...int32) *apis.JobInfo {
	return makeJobInfo(vcbatch.Restarting,
		withMaxRetry(maxRetry),
		withTaskReplicas(taskReplicas...))
}

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

func TestRestartingState_UpdateStatus_MeetsMinAvailable(t *testing.T) {
	tests := []struct {
		name         string
		taskReplicas []int32
		terminating  int32
		minAvailable int32
	}{
		{
			name:         "no terminating pods, exactly meets minAvailable",
			taskReplicas: []int32{3, 2},
			terminating:  0,
			minAvailable: 5,
		},
		{
			name:         "some terminating pods, still meets minAvailable",
			taskReplicas: []int32{3, 2},
			terminating:  2,
			minAvailable: 2,
		},
		{
			name:         "single task, minAvailable is zero",
			taskReplicas: []int32{3},
			terminating:  3,
			minAvailable: 0,
		},
		{
			name:         "total exceeds minAvailable",
			taskReplicas: []int32{5},
			terminating:  1,
			minAvailable: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &restartingState{job: makeRestartingJobInfo(10, tc.taskReplicas...)}
			status := &vcbatch.JobStatus{
				RetryCount:   1,
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

func TestRestartingState_UpdateStatus_StillWaiting(t *testing.T) {
	tests := []struct {
		name         string
		taskReplicas []int32
		terminating  int32
		minAvailable int32
	}{
		{
			name:         "one pod short after terminating subtracted",
			taskReplicas: []int32{3},
			terminating:  1,
			minAvailable: 3,
		},
		{
			name:         "all pods are terminating",
			taskReplicas: []int32{4},
			terminating:  4,
			minAvailable: 1,
		},
		{
			name:         "no tasks at all",
			taskReplicas: []int32{},
			terminating:  0,
			minAvailable: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &restartingState{job: makeRestartingJobInfo(10, tc.taskReplicas...)}
			status := &vcbatch.JobStatus{
				RetryCount:   1,
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

// MaxRetry check must run before the minAvailable check, even when both
// conditions hold simultaneously.
func TestRestartingState_UpdateStatus_MaxRetryTakesPrecedence(t *testing.T) {
	s := &restartingState{job: makeRestartingJobInfo(3, 5)}
	status := &vcbatch.JobStatus{
		RetryCount:   3,
		Terminating:  0,
		MinAvailable: 1,
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

func TestRestartingState_Execute_SyncJobCallsSyncJob(t *testing.T) {
	c := captureSyncJob(t, nil)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job == nil {
		t.Fatal("SyncJob was not called")
	}
}

func TestRestartingState_Execute_SyncJobPassesJobInfo(t *testing.T) {
	c := captureSyncJob(t, nil)
	info := makeRestartingJobInfo(5, 3)
	s := &restartingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job != info {
		t.Errorf("SyncJob received wrong JobInfo: got %p, want %p", c.job, info)
	}
}

// MaxRetry==RetryCount makes restartingUpdateStatus produce Failed; we use
// that signature to verify SyncJob received the right updateFn.
func TestRestartingState_Execute_SyncJobUpdateFnIsRestartingUpdateStatus(t *testing.T) {
	c := captureSyncJob(t, nil)
	s := &restartingState{job: makeRestartingJobInfo(2, 3)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 2}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Failed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Failed)
	}
}

func TestRestartingState_Execute_SyncJobPropagatesError(t *testing.T) {
	want := errors.New("sync failed")
	captureSyncJob(t, want)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

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
			c := captureKillTarget(t, nil)
			s := &restartingState{job: makeRestartingJobInfo(5, 3)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if c.job == nil {
				t.Fatal("KillTarget was not called")
			}
		})
	}
}

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
			c := captureKillTarget(t, nil)
			s := &restartingState{job: makeRestartingJobInfo(5, 3)}

			if err := s.Execute(Action{Action: tc.action, Target: tc.target}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.target != tc.target {
				t.Errorf("KillTarget received target %+v, want %+v", c.target, tc.target)
			}
		})
	}
}

func TestRestartingState_Execute_RestartTargetUpdateFnIsRestartingUpdateStatus(t *testing.T) {
	c := captureKillTarget(t, nil)
	s := &restartingState{job: makeRestartingJobInfo(2, 3)}

	if err := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 2}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Failed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Failed)
	}
}

func TestRestartingState_Execute_RestartTargetPropagatesError(t *testing.T) {
	want := errors.New("kill target failed")
	captureKillTarget(t, want)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if got := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

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
			c := captureKillJob(t, nil)
			s := &restartingState{job: makeRestartingJobInfo(5, 3)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if c.job == nil {
				t.Fatal("KillJob was not called")
			}
		})
	}
}

// Default branch uses PodRetainPhaseNone so all pods (including completed)
// are killed for a clean restart.
func TestRestartingState_Execute_DefaultUsesRetainPhaseNone(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.podRetainPhase) != 0 {
		t.Errorf("podRetainPhase should be empty (PhaseNone), got %v", c.podRetainPhase)
	}
}

func TestRestartingState_Execute_DefaultPassesJobInfo(t *testing.T) {
	c := captureKillJob(t, nil)
	info := makeRestartingJobInfo(5, 3)
	s := &restartingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job != info {
		t.Errorf("KillJob received wrong JobInfo: got %p, want %p", c.job, info)
	}
}

func TestRestartingState_Execute_DefaultUpdateFnIsRestartingUpdateStatus(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &restartingState{job: makeRestartingJobInfo(2, 3)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 2}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Failed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Failed)
	}
}

func TestRestartingState_Execute_DefaultPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &restartingState{job: makeRestartingJobInfo(5, 3)}

	if got := s.Execute(Action{Action: v1alpha1.RestartJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}
