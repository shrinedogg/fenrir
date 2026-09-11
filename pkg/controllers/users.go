package controllers

import (
	"context"
	"fmt"
	"reflect"

	v1alpha1types "games-on-whales.github.io/direwolf/pkg/api/v1alpha1"
	v1alpha1client "games-on-whales.github.io/direwolf/pkg/generated/clientset/versioned/typed/api/v1alpha1"
	"games-on-whales.github.io/direwolf/pkg/generic"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type UserControllerOptions struct {
	ProxyServiceName string // name of the moonlight-proxy LoadBalancer Service
}

// UserController reconciles User CRs and publishes the proxy pin URL in
// User.Status.PublicPinURL. The pin page served at that URL carries the
// username, so each user pairs as themselves instead of one hardcoded
// username per cluster.
type UserController struct {
	UserClient   v1alpha1client.UserInterface
	UserInformer generic.Informer[*v1alpha1types.User]
	K8sClient    kubernetes.Interface

	controller        generic.Controller[*v1alpha1types.User]
	serviceController generic.Controller[*corev1.Service]
	UserControllerOptions
}

func NewUserController(
	k8sClient kubernetes.Interface,
	userClient v1alpha1client.UserInterface,
	userInformer generic.Informer[*v1alpha1types.User],
	serviceInformer generic.Informer[*corev1.Service],
	options UserControllerOptions,
) *UserController {
	c := &UserController{
		K8sClient:             k8sClient,
		UserClient:            userClient,
		UserInformer:          userInformer,
		UserControllerOptions: options,
	}

	c.controller = generic.NewController(
		userInformer,
		c.Reconcile,
		generic.ControllerOptions{
			Name:    "user-controller",
			Workers: 1,
		},
	)

	// Watch the proxy Service and re-enqueue every user when it changes so
	// PublicPinURL tracks the Service IP.
	c.serviceController = generic.NewController(
		serviceInformer,
		func(namespace, name string, newObj *corev1.Service) error {
			if newObj == nil || newObj.Name != c.ProxyServiceName {
				return nil
			}
			klog.V(4).Infof("Proxy service %s/%s changed, enqueueing all users to update URLs", namespace, name)
			users, err := c.UserInformer.Namespaced(namespace).List(labels.Everything())
			if err != nil {
				return fmt.Errorf("failed to list users: %w", err)
			}
			for _, user := range users {
				c.controller.Enqueue(user.Namespace, user.Name)
			}
			return nil
		},
		generic.ControllerOptions{
			Name:    "user-controller-service-watcher",
			Workers: 1,
		},
	)

	return c
}

func (c *UserController) Run(ctx context.Context) error {
	userCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if !cache.WaitForCacheSync(userCtx.Done(), c.UserInformer.HasSynced) {
		return fmt.Errorf("failed to sync user informer")
	}

	go func() {
		defer cancel()
		if err := c.serviceController.Run(userCtx); err != nil && !k8serrors.IsNotFound(err) {
			klog.Errorf("Failed to run user service watcher: %v", err)
		}
	}()

	return c.controller.Run(userCtx)
}

func (c *UserController) Reconcile(namespace, name string, user *v1alpha1types.User) error {
	klog.V(2).Infof("Reconciling user %s/%s", namespace, name)

	if user == nil {
		// User was deleted, nothing to do.
		return nil
	}

	oldStatus := user.Status.DeepCopy()

	svc, err := c.K8sClient.CoreV1().Services(namespace).Get(context.TODO(), c.ProxyServiceName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("Proxy service '%s' not found in namespace '%s'. Cannot populate PublicPinURL for user '%s'.", c.ProxyServiceName, namespace, name)
			return nil
		}
		return fmt.Errorf("failed to get proxy service: %w", err)
	}

	address := c.getPublicAddress(svc)
	var publicURL string
	if address != "" {
		publicURL = fmt.Sprintf("http://%s/pin/", address)
	}

	user.Status.PublicPinURL = publicURL

	if !reflect.DeepEqual(&user.Status, oldStatus) {
		klog.Infof("Updating user %s/%s status with PublicPinURL: %s", namespace, name, publicURL)
		if _, err := c.UserClient.UpdateStatus(context.TODO(), user, metav1.UpdateOptions{
			FieldManager: "user-controller-status",
		}); err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to update user status: %w", err)
		}
	}

	return nil
}

// getPublicAddress extracts the external IP/hostname and pin port from the
// proxy Service for use in the pairing pin URL.
func (c *UserController) getPublicAddress(svc *corev1.Service) string {
	var host string
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		if svc.Status.LoadBalancer.Ingress[0].IP != "" {
			host = svc.Status.LoadBalancer.Ingress[0].IP
		} else if svc.Status.LoadBalancer.Ingress[0].Hostname != "" {
			host = svc.Status.LoadBalancer.Ingress[0].Hostname
		}
	}

	port := int32(47989)
	for _, p := range svc.Spec.Ports {
		if p.Port == 47989 || p.Name == "http" {
			port = p.Port
			break
		}
	}

	if host == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", host, port)
}
