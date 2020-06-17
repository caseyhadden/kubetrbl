package main

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/deprecated/scheme"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// K8sContext contains data about the path the user took through the troubleshooting
type K8sContext struct {
	kubeConfigPath string
	k8sClient      *kubernetes.Clientset
	namespace      string

	config        *rest.Config
	pods          []corev1.Pod
	svc           corev1.Service
	svcPort       corev1.ServicePort
	controller    *appsv1.Deployment
	containerPort corev1.ContainerPort
	//podList       []corev1.Pod
	podPort corev1.ContainerPort
}

func NewK8sContext(config string) *K8sContext {
	return &K8sContext{
		kubeConfigPath: config,
	}
}

func (k *K8sContext) InitClient() error {
	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", k.kubeConfigPath)
	if err != nil {
		return err
	}
	k.config = config

	// TODO this hack matches that in k8s.io/kubectl/pkg/cmd/util/kubectl_match_version.go
	if nil == k.config.GroupVersion {
		k.config.GroupVersion = &schema.GroupVersion{Group: "", Version: "v1"}
	}

	if k.config.APIPath == "" {
		k.config.APIPath = "/api"
	}
	if k.config.NegotiatedSerializer == nil {
		// This codec factory ensures the resources are not converted. Therefore, resources
		// will not be round-tripped through internal versions. Defaulting does not happen
		// on the client.
		k.config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	}
	// TODO end hack

	// create the clientset
	k.k8sClient, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	return nil
}

func (k *K8sContext) getNamespaces() ([]string, error) {
	nms, err := k.k8sClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return []string{}, err
	}

	result := []string{}
	for _, nm := range nms.Items {
		result = append(result, nm.GetName())
	}
	return result, nil
}

func (k *K8sContext) GetPods() ([]corev1.Pod, error) {
	podList, err := k.k8sClient.CoreV1().Pods(k.namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return []corev1.Pod{}, err
	}

	pods := []corev1.Pod{}
	for _, p := range podList.Items {
		pods = append(pods, p)
	}
	k.pods = pods
	return pods, nil
}

func (k *K8sContext) GetPendingPods() ([]string, error) {
	result := []string{}
	for _, pod := range k.pods {
		status := pod.Status
		if status.Phase == corev1.PodPending {
			result = append(result, pod.GetName())
		}
	}
	return result, nil
}

func (k *K8sContext) GetNonrunningPods() ([]string, error) {
	result := []string{}
	for _, pod := range k.pods {
		status := pod.Status
		if status.Phase != corev1.PodRunning {
			result = append(result, pod.GetName())
		}
	}
	return result, nil
}

func (k *K8sContext) GetNotReadyPods() ([]string, error) {
	result := []string{}
	for _, pod := range k.pods {
		status := pod.Status
		for _, c := range status.Conditions {
			if c.Type == corev1.PodReady && c.Status != corev1.ConditionTrue {
				result = append(result, pod.GetName())
			}
		}
	}
	return result, nil
}

func (k *K8sContext) GetServices() ([]string, error) {
	result := []string{}

	svcs, err := k.k8sClient.CoreV1().Services(k.namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return result, err
	}

	for _, s := range svcs.Items {
		result = append(result, s.GetName())
	}

	return result, nil
}
