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
)

func TestAbortedState_Execute_ResumeCallsKillJob(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortedState{job: makeJobInfo(vcbatch.Aborted)}

	if err := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job == nil {
		t.Fatal("KillJob was not called")
	}
}

func TestAbortedState_Execute_ResumeUsesSoftRetainPhase(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortedState{job: makeJobInfo(vcbatch.Aborted)}

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

func TestAbortedState_Execute_ResumeUpdateFnSetsRestarting(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortedState{job: makeJobInfo(vcbatch.Aborted)}

	if err := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn == nil {
		t.Fatal("expected non-nil updateFn for ResumeJobAction")
	}

	status := &vcbatch.JobStatus{State: vcbatch.JobState{Phase: vcbatch.Aborted}}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true (phase changed)")
	}
	if status.State.Phase != vcbatch.Restarting {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Restarting)
	}
}

func TestAbortedState_Execute_ResumeUpdateFnIncrementsRetryCount(t *testing.T) {
	tests := []struct {
		name         string
		initialRetry int32
		wantRetry    int32
	}{
		{"from zero", 0, 1},
		{"from non-zero", 3, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &abortedState{job: makeJobInfo(vcbatch.Aborted)}

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

func TestAbortedState_Execute_ResumePassesJobInfo(t *testing.T) {
	c := captureKillJob(t, nil)
	info := makeJobInfo(vcbatch.Aborted)
	s := &abortedState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job != info {
		t.Errorf("KillJob received wrong JobInfo: got %p, want %p", c.job, info)
	}
}

func TestAbortedState_Execute_ResumePropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &abortedState{job: makeJobInfo(vcbatch.Aborted)}

	if got := s.Execute(Action{Action: v1alpha1.ResumeJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

func TestAbortedState_Execute_DefaultActionsCallKillJob(t *testing.T) {
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
			s := &abortedState{job: makeJobInfo(vcbatch.Aborted)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if c.job == nil {
				t.Fatal("KillJob was not called")
			}
		})
	}
}

// Default branch must pass nil updateFn so KillJob does not change the phase.
func TestAbortedState_Execute_DefaultNilUpdateFn(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortedState{job: makeJobInfo(vcbatch.Aborted)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn != nil {
		t.Error("default branch should pass nil updateFn to KillJob")
	}
}

func TestAbortedState_Execute_DefaultUsesSoftRetainPhase(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &abortedState{job: makeJobInfo(vcbatch.Aborted)}

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

func TestAbortedState_Execute_DefaultPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &abortedState{job: makeJobInfo(vcbatch.Aborted)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}
