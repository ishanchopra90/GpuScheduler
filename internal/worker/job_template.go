package worker

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
)

// DefaultWorkerImage is the container image used for the worker Job when none is specified.
// The container is expected to accept SIMULATOR_URL, WORKLOAD_NAMESPACE, WORKLOAD_NAME
// and call the simulator Allocate/Start then poll until completion and exit.
const DefaultWorkerImage = "gpu-scheduler-worker:latest"

// Env var names the worker container expects.
const (
	EnvSimulatorURL      = "SIMULATOR_URL"
	EnvWorkloadName      = "WORKLOAD_NAME"
	EnvWorkloadNamespace = "WORKLOAD_NAMESPACE"
)

// WorkerJobTemplate defines the Job and Pod template used when creating a one-Job-per-workload
// worker. The operator (or caller) fills in Job name, namespace, owner reference, and env
// values; the container runs to completion then exits.
type WorkerJobTemplate struct {
	// Image is the worker container image (defaults to DefaultWorkerImage).
	Image string
	// BackoffLimit is the Job spec.backoffLimit (default 0 so Job fails after one pod failure).
	BackoffLimit int32
}

// DefaultWorkerJobTemplate returns a template with DefaultWorkerImage and BackoffLimit 0.
func DefaultWorkerJobTemplate() WorkerJobTemplate {
	return WorkerJobTemplate{
		Image:        DefaultWorkerImage,
		BackoffLimit: 0,
	}
}

// BuildWorkerJob returns a batch/v1 Job for the given GPUWorkload. The Job runs one Pod
// that executes the worker image with env set from workload identity and simulatorURL.
// Caller should set the Job's owner reference to the workload for GC.
func BuildWorkerJob(workload *schedulerv1alpha1.GPUWorkload, simulatorURL string, tpl WorkerJobTemplate) *batchv1.Job {
	if tpl.Image == "" {
		tpl.Image = DefaultWorkerImage
	}
	jobName := workload.Name + "-job"
	env := []corev1.EnvVar{
		{Name: EnvSimulatorURL, Value: simulatorURL},
		{Name: EnvWorkloadNamespace, Value: workload.Namespace},
		{Name: EnvWorkloadName, Value: workload.Name},
	}
	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:  "worker",
				Image: tpl.Image,
				Env:   env,
			},
		},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: workload.Namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &tpl.BackoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":             "gpu-scheduler-worker",
						"app.kubernetes.io/component":        "worker",
						"scheduler.ishanchopra.dev/workload": workload.Name,
					},
				},
				Spec: podSpec,
			},
		},
	}
	// Owner reference to the GPUWorkload should be set by the caller when creating the Job
	// so the Job is garbage-collected when the workload is deleted.
	return job
}
