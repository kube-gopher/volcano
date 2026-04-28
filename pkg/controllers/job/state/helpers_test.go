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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"

	"volcano.sh/volcano/pkg/controllers/apis"
)

func int32Ptr(v int32) *int32 { return &v }

type jobOption func(*vcbatch.Job)

func withMinAvailable(n int32) jobOption {
	return func(j *vcbatch.Job) { j.Spec.MinAvailable = n }
}

func withMinSuccess(n *int32) jobOption {
	return func(j *vcbatch.Job) { j.Spec.MinSuccess = n }
}

func withMaxRetry(n int32) jobOption {
	return func(j *vcbatch.Job) { j.Spec.MaxRetry = n }
}

func withTasks(tasks ...vcbatch.TaskSpec) jobOption {
	return func(j *vcbatch.Job) { j.Spec.Tasks = tasks }
}

func withTaskReplicas(replicas ...int32) jobOption {
	tasks := make([]vcbatch.TaskSpec, len(replicas))
	for i, r := range replicas {
		tasks[i] = vcbatch.TaskSpec{Replicas: r}
	}
	return withTasks(tasks...)
}

func makeJobInfo(phase vcbatch.JobPhase, opts ...jobOption) *apis.JobInfo {
	job := &vcbatch.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "test-job", Namespace: "default"},
		Status:     vcbatch.JobStatus{State: vcbatch.JobState{Phase: phase}},
	}
	for _, opt := range opts {
		opt(job)
	}
	return &apis.JobInfo{Job: job}
}

type capturedKillJob struct {
	job            *apis.JobInfo
	podRetainPhase PhaseMap
	updateFn       UpdateStatusFn
}

func captureKillJob(t *testing.T, returnErr error) *capturedKillJob {
	t.Helper()
	original := KillJob
	c := &capturedKillJob{}
	KillJob = func(job *apis.JobInfo, podRetainPhase PhaseMap, fn UpdateStatusFn) error {
		c.job = job
		c.podRetainPhase = podRetainPhase
		c.updateFn = fn
		return returnErr
	}
	t.Cleanup(func() { KillJob = original })
	return c
}

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
