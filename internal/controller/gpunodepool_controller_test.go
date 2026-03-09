package controller

import (
	"context"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/sim"
	"github.com/ishanchopra/gpu-scheduler/mocks"
)

var _ = Describe("GPUNodePool Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		gpunodepool := &schedulerv1alpha1.GPUNodePool{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind GPUNodePool")
			err := k8sClient.Get(ctx, typeNamespacedName, gpunodepool)
			if err != nil && errors.IsNotFound(err) {
				resource := &schedulerv1alpha1.GPUNodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: schedulerv1alpha1.GPUNodePoolSpec{
						NodeCount:          2,
						DevicesPerNode:     4,
						MemoryMiBPerDevice: 81920,
						Profile:            "h100_sxm",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &schedulerv1alpha1.GPUNodePool{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance GPUNodePool")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			ctrl := gomock.NewController(GinkgoT())
			mockFleet := mocks.NewMockFleetRegistrar(ctrl)
			expectedSpec := sim.GPUNodePoolSpec{
				NodeCount:          2,
				DevicesPerNode:     4,
				MemoryMiBPerDevice: 81920,
				Profile:            "h100_sxm",
				DeviceType:         "gpu", // CRD default when not set
			}
			usage := sim.FleetUsage{
				TotalDevices:     8,
				AllocatedDevices: 0,
				AvailableDevices: 8,
				Utilization:      0,
			}
			poolKey := "default/" + resourceName
			mockFleet.EXPECT().
				RegisterFleet(poolKey, gomock.AssignableToTypeOf(sim.GPUNodePoolSpec{})).
				DoAndReturn(func(key string, spec sim.GPUNodePoolSpec) error {
					Expect(key).To(Equal(poolKey))
					Expect(spec).To(Equal(expectedSpec))
					return nil
				}).
				Times(1)
			mockFleet.EXPECT().
				FleetUsageForPool(poolKey).
				Return(usage, true).
				Times(1)

			controllerReconciler := &GPUNodePoolReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				FleetRegistrar: mockFleet,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &schedulerv1alpha1.GPUNodePool{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.TotalDevices).To(Equal(int32(8)))
			Expect(updated.Status.AllocatedDevices).To(Equal(int32(0)))
			Expect(updated.Status.AvailableDevices).To(Equal(int32(8)))
			Expect(updated.Status.UtilizationPct).To(Equal(int32(0)))
		})
	})
})
