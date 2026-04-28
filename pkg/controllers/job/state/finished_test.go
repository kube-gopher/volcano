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

	v1 "k8s.io/api/core/v1"

	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"
	"volcano.sh/apis/pkg/apis/bus/v1alpha1"
)

func TestFinishedState_Execute_ActionAgnostic(t *testing.T) {
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
			c := captureKillJob(t, nil)
			info := makeJobInfo(vcbatch.Completed)
			s := &finishedState{job: info}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if c.job == nil {
				t.Fatal("KillJob was not called")
			}
		})
	}
}

func TestFinishedState_Execute_UsesSoftRetainPhase(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &finishedState{job: makeJobInfo(vcbatch.Completed)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, phase := range []v1.PodPhase{v1.PodSucceeded, v1.PodFailed} {
		if _, ok := c.podRetainPhase[phase]; !ok {
			t.Errorf("podRetainPhase missing %q; got %v", phase, c.podRetainPhase)
		}
	}
	if len(c.podRetainPhase) != 2 {
		t.Errorf("podRetainPhase should have exactly 2 entries, got %d", len(c.podRetainPhase))
	}
}

// finishedState is terminal: updateFn must be nil so KillJob never mutates phase.
func TestFinishedState_Execute_NilUpdateFn(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &finishedState{job: makeJobInfo(vcbatch.Completed)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn != nil {
		t.Error("KillJob updateFn should be nil for finishedState")
	}
}

func TestFinishedState_Execute_PassesJobInfo(t *testing.T) {
	c := captureKillJob(t, nil)
	info := makeJobInfo(vcbatch.Terminated)
	s := &finishedState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job != info {
		t.Errorf("KillJob received wrong JobInfo: got %p, want %p", c.job, info)
	}
}

func TestFinishedState_Execute_PropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &finishedState{job: makeJobInfo(vcbatch.Failed)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}
