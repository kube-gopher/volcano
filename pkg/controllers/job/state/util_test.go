/*
Copyright 2024 The Volcano Authors.

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
	"testing"

	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"
)

func int32Ptr(v int32) *int32 { return &v }

func TestUtilTotalTasks_NoTasks(t *testing.T) {
	job := &vcbatch.Job{}
	if got := TotalTasks(job); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestUtilTotalTasks_SingleTask(t *testing.T) {
	tests := []struct {
		name     string
		replicas int32
		want     int32
	}{
		{"zero replicas", 0, 0},
		{"one replica", 1, 1},
		{"many replicas", 5, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := &vcbatch.Job{
				Spec: vcbatch.JobSpec{
					Tasks: []vcbatch.TaskSpec{
						{Replicas: tc.replicas},
					},
				},
			}
			if got := TotalTasks(job); got != tc.want {
				t.Errorf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestUtilTotalTasks_MultipleTasks(t *testing.T) {
	tests := []struct {
		name  string
		tasks []vcbatch.TaskSpec
		want  int32
	}{
		{
			name: "two tasks summed",
			tasks: []vcbatch.TaskSpec{
				{Replicas: 3},
				{Replicas: 4},
			},
			want: 7,
		},
		{
			name: "zero in middle ignored",
			tasks: []vcbatch.TaskSpec{
				{Replicas: 2},
				{Replicas: 0},
				{Replicas: 5},
			},
			want: 7,
		},
		{
			name: "all zero replicas",
			tasks: []vcbatch.TaskSpec{
				{Replicas: 0},
				{Replicas: 0},
			},
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := &vcbatch.Job{Spec: vcbatch.JobSpec{Tasks: tc.tasks}}
			if got := TotalTasks(job); got != tc.want {
				t.Errorf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestUtilTotalTaskMinAvailable_NoTasks(t *testing.T) {
	job := &vcbatch.Job{}
	if got := TotalTaskMinAvailable(job); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

// MinAvailable == nil → falls back to Replicas.
func TestUtilTotalTaskMinAvailable_MinAvailableNil(t *testing.T) {
	tests := []struct {
		name  string
		tasks []vcbatch.TaskSpec
		want  int32
	}{
		{
			name:  "single task nil minAvailable uses replicas",
			tasks: []vcbatch.TaskSpec{{Replicas: 3, MinAvailable: nil}},
			want:  3,
		},
		{
			name: "multiple tasks all nil minAvailable",
			tasks: []vcbatch.TaskSpec{
				{Replicas: 2, MinAvailable: nil},
				{Replicas: 4, MinAvailable: nil},
			},
			want: 6,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := &vcbatch.Job{Spec: vcbatch.JobSpec{Tasks: tc.tasks}}
			if got := TotalTaskMinAvailable(job); got != tc.want {
				t.Errorf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

// MinAvailable set → uses MinAvailable value, ignores Replicas.
func TestUtilTotalTaskMinAvailable_MinAvailableSet(t *testing.T) {
	tests := []struct {
		name  string
		tasks []vcbatch.TaskSpec
		want  int32
	}{
		{
			name:  "minAvailable less than replicas",
			tasks: []vcbatch.TaskSpec{{Replicas: 5, MinAvailable: int32Ptr(2)}},
			want:  2,
		},
		{
			name:  "minAvailable equals replicas",
			tasks: []vcbatch.TaskSpec{{Replicas: 3, MinAvailable: int32Ptr(3)}},
			want:  3,
		},
		{
			name:  "minAvailable is zero",
			tasks: []vcbatch.TaskSpec{{Replicas: 4, MinAvailable: int32Ptr(0)}},
			want:  0,
		},
		{
			name: "multiple tasks all with minAvailable",
			tasks: []vcbatch.TaskSpec{
				{Replicas: 5, MinAvailable: int32Ptr(2)},
				{Replicas: 4, MinAvailable: int32Ptr(1)},
			},
			want: 3,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := &vcbatch.Job{Spec: vcbatch.JobSpec{Tasks: tc.tasks}}
			if got := TotalTaskMinAvailable(job); got != tc.want {
				t.Errorf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

// Mixed: some tasks have MinAvailable set, others don't.
func TestUtilTotalTaskMinAvailable_Mixed(t *testing.T) {
	tests := []struct {
		name  string
		tasks []vcbatch.TaskSpec
		want  int32
	}{
		{
			name: "first set second nil",
			tasks: []vcbatch.TaskSpec{
				{Replicas: 5, MinAvailable: int32Ptr(2)},
				{Replicas: 3, MinAvailable: nil},
			},
			want: 5, // 2 (minAvailable) + 3 (replicas fallback)
		},
		{
			name: "first nil second set",
			tasks: []vcbatch.TaskSpec{
				{Replicas: 4, MinAvailable: nil},
				{Replicas: 6, MinAvailable: int32Ptr(1)},
			},
			want: 5, // 4 (replicas fallback) + 1 (minAvailable)
		},
		{
			name: "three tasks alternating",
			tasks: []vcbatch.TaskSpec{
				{Replicas: 3, MinAvailable: int32Ptr(2)},
				{Replicas: 5, MinAvailable: nil},
				{Replicas: 2, MinAvailable: int32Ptr(0)},
			},
			want: 7, // 2 + 5 + 0
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := &vcbatch.Job{Spec: vcbatch.JobSpec{Tasks: tc.tasks}}
			if got := TotalTaskMinAvailable(job); got != tc.want {
				t.Errorf("expected %d, got %d", tc.want, got)
			}
		})
	}
}
