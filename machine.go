package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/caseyhadden/kubetrbl/fsm"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type Kubetrbl struct {
	fsm        *fsm.FSM
	reader     *bufio.Reader
	k8sContext *K8sContext

	svc           corev1.Service
	svcPort       corev1.ServicePort
	controller    *appsv1.Deployment
	containerPort corev1.ContainerPort
	podList       []corev1.Pod
	podPort       corev1.ContainerPort
	pods          *corev1.PodList
}

func NewKubetrbl() *Kubetrbl {
	k := &Kubetrbl{
		reader: bufio.NewReader(os.Stdin),
	}

	machine := fsm.NewFSM()
	// generic error state
	machine.ErrorHandler = func(f *fsm.FSM, err error) {
		fmt.Println("An error occurred when troubleshooting your Kubernetes deployment.")
		fmt.Println(err.Error())
		// re-enter original state
		f.Change(f.State)
	}

	machine.Register("welcome", fsm.State{Enter: k.welcome})
	machine.Register("finish", fsm.State{Enter: k.finish})
	machine.Register("getKubeConfig", fsm.State{Enter: k.getKubeConfig, Update: k.createK8sClient})
	machine.Register("getNamespace", fsm.State{Enter: k.getNamespace})
	machine.Register("countPods", fsm.State{Enter: k.countPods})
	machine.Register("checkPendingPods", fsm.State{Enter: k.checkPendingPods})
	machine.Register("checkRunningPods", fsm.State{Enter: k.checkRunningPods})
	machine.Register("checkReadyPods", fsm.State{Enter: k.checkReadyPods})
	machine.Register("getServiceName", fsm.State{Enter: k.getServiceName})
	machine.Register("getServicePort", fsm.State{Enter: k.getServicePort})
	machine.Register("getControllerWorkload", fsm.State{Enter: k.getControllerWorkload})
	machine.Register("getContainerPort", fsm.State{Enter: k.getContainerPort})
	machine.Register("getControllerPods", fsm.State{Enter: k.getControllerPods})
	machine.Register("validateContainerPort", fsm.State{Enter: k.validateContainerPort})

	k.fsm = machine

	return k
}

// Start initialized our state machine and sets us to the first state
func (k *Kubetrbl) Start() {
	k.fsm.Change("welcome")
}

func (k *Kubetrbl) finish() error {
	fmt.Println("See ya!")
	return nil
}

func (k *Kubetrbl) welcome() error {
	fmt.Println("Wecome to Kubetrbl.")
	fmt.Println("Kubetrbl aims to provide a guided method for troubleshooting a Kubernetes deployment.")
	fmt.Println("Kubetrbl's actions are based off of the troubleshooting flow described at https://learnk8s.io/a/troubleshooting-kubernetes.pdf.")
	fmt.Println()
	k.fsm.Change("getKubeConfig")
	return nil
}

func (k *Kubetrbl) getKubeConfig() error {
	fmt.Println("We need to start by connecting to a Kubernetes cluster.")
	fmt.Println("Enter the location of your KUBECONFIG file: ")
	cfg, err := k.readString()
	if err != nil {
		return err
	}
	k.k8sContext = NewK8sContext(cfg)
	k.fsm.Update()
	return nil
}

func (k *Kubetrbl) createK8sClient() error {
	err := k.k8sContext.InitClient()
	if err != nil {
		return err
	}
	k.fsm.Change("getNamespace")
	return nil
}

func (k *Kubetrbl) getNamespace() error {
	nms, err := k.k8sContext.getNamespaces()
	if err != nil {
		return err
	}
	fmt.Println("Available namespaces:")
	for i, nm := range nms {
		fmt.Println(strconv.Itoa(i) + ") " + nm)
	}
	fmt.Printf("Kubernetes namespace? ")
	answer, err := k.readInt()
	if err != nil {
		return err
	}
	k.k8sContext.namespace = nms[answer]
	k.fsm.Change("countPods")
	return nil
}

func (k *Kubetrbl) countPods() error {
	pods, err := k.k8sContext.GetPods()
	if err != nil {
		return err
	}
	fmt.Printf("There are %d pods in the cluster+namespace.\n", len(pods))
	k.fsm.Change("checkPendingPods")
	return nil
}

func (k *Kubetrbl) checkPendingPods() error {
	pendingPods, err := k.k8sContext.GetPendingPods()
	if err != nil {
		return err
	}

	if len(pendingPods) > 0 {
		for _, p := range pendingPods {
			fmt.Println("\u2717 Pending - " + p)
		}
		// TODO transition for pending pods
	} else {
		fmt.Println("\u2713 No pods are pending.")
		k.fsm.Change("checkRunningPods")
	}

	return nil
}

func (k *Kubetrbl) checkRunningPods() error {
	nonrunningPods, err := k.k8sContext.GetNonrunningPods()
	if err != nil {
		return err
	}

	if len(nonrunningPods) > 0 {
		for _, p := range nonrunningPods {
			fmt.Println("\u2717 Not running - " + p)
		}
		// TODO transition for non-running pods
	} else {
		fmt.Println("\u2713 All pods are running.")
		k.fsm.Change("checkReadyPods")
	}
	return nil
}

func (k *Kubetrbl) checkReadyPods() error {
	notReadyPods, err := k.k8sContext.GetNotReadyPods()
	if err != nil {
		return err
	}

	if len(notReadyPods) > 0 {
		for _, p := range notReadyPods {
			fmt.Println("\u2717 Not ready - " + p)
		}
		// TODO transition for non-running pods
	} else {
		fmt.Println("\u2713 All pods are ready.")
		k.fsm.Change("getServiceName")
	}
	return nil
}

func (k *Kubetrbl) getServiceName() error {
	svcs, err := k.k8sContext.k8sClient.CoreV1().Services(k.k8sContext.namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	fmt.Println("Available services: ")
	for i, s := range svcs.Items {
		fmt.Println(strconv.Itoa(i) + ") " + s.GetName())
	}

	fmt.Printf("Which service? ")
	answer, err := k.readInt()
	if err != nil {
		return err
	}

	k.svc = svcs.Items[answer]

	k.fsm.Change("getServicePort")
	return nil
}

func (k *Kubetrbl) getServicePort() error {
	fmt.Println("Available ports: ")
	for i, p := range k.svc.Spec.Ports {
		fmt.Println(strconv.Itoa(i) + ") " + p.Name)
	}

	fmt.Printf("Which port? ")
	answer, err := k.readInt()
	if err != nil {
		return err
	}

	k.svcPort = k.svc.Spec.Ports[answer]
	k.fsm.Change("getControllerWorkload")
	return nil
}

func (k *Kubetrbl) getControllerWorkload() error {
	k8sName := k.svc.Spec.Selector["app.kubernetes.io/name"]
	deployment, err := k.k8sContext.k8sClient.AppsV1().Deployments(k.k8sContext.namespace).Get(context.TODO(), k8sName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	k.controller = deployment
	fmt.Println("\u2713 Found backing Deployment - " + k.controller.GetName())
	k.fsm.Change("getContainerPort")
	return nil
}

func (k *Kubetrbl) getContainerPort() error {
	tgt := k.svcPort.TargetPort.StrVal
	for _, cnt := range k.controller.Spec.Template.Spec.Containers {
		for _, p := range cnt.Ports {
			if tgt == p.Name {
				k.containerPort = p
				break
			}
		}
	}
	fmt.Println("\u2713 Identified pod port: " + strconv.Itoa(int(k.containerPort.ContainerPort)))
	k.fsm.Change("getControllerPods")
	return nil
}

func (k *Kubetrbl) getControllerPods() error {
	// our target is based off the controller
	tgt := k.controller.Labels["app.kubernetes.io/name"]
	result := []corev1.Pod{}
	for _, p := range k.k8sContext.pods {
		pos := p.Labels["app.kubernetes.io/name"]
		if tgt == pos {
			result = append(result, p)
		}
	}
	k.podList = result
	k.fsm.Change("validateContainerPort")
	return nil
}

func (k *Kubetrbl) validateContainerPort() error {
	client, err := rest.RESTClientFor(k.k8sContext.config)
	if err != nil {
		return err
	}

	for _, pod := range k.podList {
		fmt.Printf("Checking accessibility of port for pod '%s'.\n", pod.Name)
		req := client.Post().
			Resource("pods").
			Namespace(k.k8sContext.namespace).
			Name(pod.Name).
			SubResource("portforward")

		// TODO retrieve local port from user
		portMapping := []string{fmt.Sprintf("%d:%d", 8080, k.containerPort.ContainerPort)}

		transport, upgrader, err := spdy.RoundTripperFor(k.k8sContext.config)
		dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())
		stopChan := make(chan struct{}, 1)
		readyChan := make(chan struct{})
		pf, err := portforward.New(
			dialer,
			portMapping,
			stopChan,
			readyChan,
			os.Stdout,
			os.Stderr,
		)
		if err != nil {
			return err
		}

		doneChan := make(chan error)
		go func() {
			doneChan <- pf.ForwardPorts()
		}()
		<-pf.Ready

		// TODO retrieve path from user
		resp, err := http.DefaultClient.Get("http://localhost:8080/internal/metrics")
		if err != nil {
			return err
		}
		if resp.StatusCode < 400 {
			fmt.Println("\u2713 Pod port accessible.")
		} else {
			// TODO transition to failure state
			fmt.Println("\u2717 Pod port inaccessible.")
		}

		close(stopChan)

	}
	k.fsm.Change("finish")
	return nil
}

func (k *Kubetrbl) readString() (string, error) {
	str, err := k.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(str), nil
}

func (k *Kubetrbl) readInt() (int, error) {
	str, err := k.readString()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(str)
}
