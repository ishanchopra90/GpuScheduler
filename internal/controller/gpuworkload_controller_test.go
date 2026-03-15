package controller

import (
	"context"
	"time"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/sim"
	"github.com/ishanchopra/gpu-scheduler/mocks"
)

var _ = Describe("GPUWorkload Controller", func() {
	Context("When reconciling a resource", func() {
		It("should successfully reconcile the resource", func() {
			// Minimal smoke test: create resource and reconcile once.
			ctx := context.Background()
			key := types.NamespacedName{Name: "smoke-workload", Namespace: "default"}
			wl := &schedulerv1alpha1.GPUWorkload{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: schedulerv1alpha1.GPUWorkloadSpec{
					Tenant:       "team-a",
					Priority:     5,
					GPUCount:     1,
					GPUMemoryMiB: 16000,
					Profile:      "h100_sxm",
					Tokens:       100,
					Kind:         schedulerv1alpha1.WorkloadKindTraining,
					Training: &schedulerv1alpha1.TrainingRuntimeSpec{
						GlobalBatchSize: 1,
						MicroBatchSize:  1,
						GradAccumSteps:  1,
						SequenceLength:  1,
					},
				},
			}
			Expect(k8sClient.Create(ctx, wl)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, wl)
			}()

			ctrl := gomock.NewController(GinkgoT())
			mockSim := mocks.NewMockWorkloadRuntimeSimulator(ctrl)
			r := &GPUWorkloadReconciler{
				Client:           k8sClient,
				Scheme:           scheme.Scheme,
				RuntimeSimulator: mockSim,
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	// Standard flow: create -> reconciler sets Queued -> (scheduler patches Scheduled) -> reconciler executes admission -> Running -> sim returns Succeeded -> reconciler sets Succeeded.
	Context("lifecycle flow (create -> Queued -> Scheduled -> Running -> Succeeded)", func() {
		const workloadName = "flow-test-workload"
		var key types.NamespacedName

		BeforeEach(func() {
			key = types.NamespacedName{Name: workloadName, Namespace: "default"}
		})

		It("runs full flow to Succeeded", func() {
			ctx := context.Background()
			workloadID := key.Namespace + "/" + key.Name
			wl := &schedulerv1alpha1.GPUWorkload{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: schedulerv1alpha1.GPUWorkloadSpec{
					Tenant:       "team-a",
					Priority:     5,
					GPUCount:     1,
					GPUMemoryMiB: 16000,
					Profile:      "h100_sxm",
					Tokens:       100,
					Kind:         schedulerv1alpha1.WorkloadKindTraining,
					Training: &schedulerv1alpha1.TrainingRuntimeSpec{
						GlobalBatchSize: 1,
						MicroBatchSize:  1,
						GradAccumSteps:  1,
						SequenceLength:  1,
					},
				},
			}
			Expect(k8sClient.Create(ctx, wl)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, &schedulerv1alpha1.GPUWorkload{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}})
			}()

			ctrl := gomock.NewController(GinkgoT())
			mockSim := mocks.NewMockWorkloadRuntimeSimulator(ctrl)
			mockSim.EXPECT().
				AllocateWithOptions(workloadID, 1, 16000, gomock.Any()).
				DoAndReturn(func(_ string, _ int, _ int, opts sim.AllocationOptions) (*sim.Allocation, error) {
					Expect(opts.PreferredProfile).To(Equal("h100_sxm"))
					return &sim.Allocation{}, nil
				}).
				Times(1)
			mockSim.EXPECT().
				StartWithRuntimeInput(gomock.Any()).
				Return("run-1", nil).
				Times(1)
			mockSim.EXPECT().
				GetLatestRunStatusForWorkload(workloadID).
				Return(sim.RunStatusSucceeded, nil).
				Times(1)
			mockSim.EXPECT().
				Release(workloadID).
				Return(nil).
				Times(1)

			r := &GPUWorkloadReconciler{
				Client:           k8sClient,
				Scheme:           scheme.Scheme,
				RuntimeSimulator: mockSim,
			}

			// 1) Reconcile: add finalizer
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			// 2) Reconcile: set Phase=Queued
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, key, wl)).To(Succeed())
			Expect(wl.Status.Phase).To(Equal(schedulerv1alpha1.GPUWorkloadPhaseQueued))
			Expect(wl.Status.QueuedAt).NotTo(BeNil())

			// 3) Simulate scheduler loop: patch to Scheduled
			wl.Status.Phase = schedulerv1alpha1.GPUWorkloadPhaseScheduled
			Expect(k8sClient.Status().Update(ctx, wl)).To(Succeed())

			// 4) Reconcile: executeAdmission -> Allocate, Start, set Running
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, key, wl)).To(Succeed())
			Expect(wl.Status.Phase).To(Equal(schedulerv1alpha1.GPUWorkloadPhaseRunning))

			// 5) Sim returns Succeeded; reconcile updates phase
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, key, wl)).To(Succeed())
			Expect(wl.Status.Phase).To(Equal(schedulerv1alpha1.GPUWorkloadPhaseSucceeded))
		})

		It("runs flow to Failed when simulator returns Failed", func() {
			ctx := context.Background()
			// Use a distinct name so this test does not collide with "runs full flow to Succeeded" (same key name = same resource).
			failedKey := types.NamespacedName{Name: "flow-test-workload-failed", Namespace: key.Namespace}
			workloadID := failedKey.Namespace + "/" + failedKey.Name
			wl := &schedulerv1alpha1.GPUWorkload{
				ObjectMeta: metav1.ObjectMeta{Name: failedKey.Name, Namespace: failedKey.Namespace},
				Spec: schedulerv1alpha1.GPUWorkloadSpec{
					Tenant:       "team-a",
					Priority:     5,
					GPUCount:     1,
					GPUMemoryMiB: 16000,
					Profile:      "h100_sxm",
					Tokens:       100,
					Kind:         schedulerv1alpha1.WorkloadKindTraining,
					Training: &schedulerv1alpha1.TrainingRuntimeSpec{
						GlobalBatchSize: 1,
						MicroBatchSize:  1,
						GradAccumSteps:  1,
						SequenceLength:  1,
					},
				},
			}
			Expect(k8sClient.Create(ctx, wl)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, &schedulerv1alpha1.GPUWorkload{ObjectMeta: metav1.ObjectMeta{Name: failedKey.Name, Namespace: failedKey.Namespace}})
			}()

			ctrl := gomock.NewController(GinkgoT())
			mockSim := mocks.NewMockWorkloadRuntimeSimulator(ctrl)
			mockSim.EXPECT().
				AllocateWithOptions(workloadID, 1, 16000, gomock.Any()).
				DoAndReturn(func(_ string, _ int, _ int, opts sim.AllocationOptions) (*sim.Allocation, error) {
					Expect(opts.PreferredProfile).To(Equal("h100_sxm"))
					return &sim.Allocation{}, nil
				}).
				Times(1)
			mockSim.EXPECT().
				StartWithRuntimeInput(gomock.Any()).
				Return("run-1", nil).
				Times(1)
			mockSim.EXPECT().
				GetLatestRunStatusForWorkload(workloadID).
				Return(sim.RunStatusFailed, nil).
				Times(1)
			mockSim.EXPECT().
				Release(workloadID).
				Return(nil).
				Times(1)

			r := &GPUWorkloadReconciler{
				Client:           k8sClient,
				Scheme:           scheme.Scheme,
				RuntimeSimulator: mockSim,
			}
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: failedKey})
			}
			Expect(k8sClient.Get(ctx, failedKey, wl)).To(Succeed())
			wl.Status.Phase = schedulerv1alpha1.GPUWorkloadPhaseScheduled
			Expect(k8sClient.Status().Update(ctx, wl)).To(Succeed())
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: failedKey})
			Expect(k8sClient.Get(ctx, failedKey, wl)).To(Succeed())
			Expect(wl.Status.Phase).To(Equal(schedulerv1alpha1.GPUWorkloadPhaseRunning))

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: failedKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, failedKey, wl)).To(Succeed())
			Expect(wl.Status.Phase).To(Equal(schedulerv1alpha1.GPUWorkloadPhaseFailed))
		})
	})

	Context("finalizer and delete", func() {
		It("adds finalizer on first reconcile and removes it on delete after releasing allocation", func() {
			ctx := context.Background()
			key := types.NamespacedName{Name: "finalizer-workload", Namespace: "default"}
			workloadID := key.Namespace + "/" + key.Name
			wl := &schedulerv1alpha1.GPUWorkload{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: schedulerv1alpha1.GPUWorkloadSpec{
					Tenant:       "team-a",
					Priority:     5,
					GPUCount:     1,
					GPUMemoryMiB: 16000,
					Profile:      "h100_sxm",
					Tokens:       100,
					Kind:         schedulerv1alpha1.WorkloadKindTraining,
					Training: &schedulerv1alpha1.TrainingRuntimeSpec{
						GlobalBatchSize: 1,
						MicroBatchSize:  1,
						GradAccumSteps:  1,
						SequenceLength:  1,
					},
				},
			}
			Expect(k8sClient.Create(ctx, wl)).To(Succeed())

			ctrl := gomock.NewController(GinkgoT())
			mockSim := mocks.NewMockWorkloadRuntimeSimulator(ctrl)
			mockSim.EXPECT().Release(workloadID).Return(nil).Times(1)
			r := &GPUWorkloadReconciler{
				Client:           k8sClient,
				Scheme:           scheme.Scheme,
				RuntimeSimulator: mockSim,
			}

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, key, wl)).To(Succeed())
			Expect(wl.Finalizers).To(ContainElement("scheduler.ishanchopra.dev/gpuworkload-finalizer"))

			Expect(k8sClient.Delete(ctx, wl)).To(Succeed())
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, key, &schedulerv1alpha1.GPUWorkload{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}, 10*time.Second, 200*time.Millisecond).Should(Succeed())
		})
	})
})
