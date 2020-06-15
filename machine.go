package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/caseyhadden/kubetrbl/fsm"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/deprecated/scheme"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type Kubetrbl struct {
	fsm    *fsm.FSM
	reader *bufio.Reader
	err    error

	cfg       string
	k8sClient *kubernetes.Clientset
	namespace string

	config *rest.Config
	pods   *corev1.PodList
	pod    corev1.Pod
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
	machine.Register("getPodName", fsm.State{Enter: k.getPodName})
	machine.Register("getPort", fsm.State{Enter: k.getPort})

	k.fsm = machine

	return k
}

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
	k.cfg = cfg
	k.fsm.Update()
	return nil
}

func (k *Kubetrbl) createK8sClient() error {
	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", k.cfg)
	if err != nil {
		return err
	}
	k.config = config

	// create the clientset
	k.k8sClient, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	k.fsm.Change("getNamespace")
	return nil
}

func (k *Kubetrbl) getNamespace() error {
	nms, err := k.k8sClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i, nm := range nms.Items {
		fmt.Println(strconv.Itoa(i) + ") " + nm.GetName())
	}
	fmt.Println("Choose the Kubernetes namespace of interest: ")
	answer, err := k.readString()
	if err != nil {
		return err
	}
	s, err := strconv.Atoi(answer)
	if err != nil {
		return err
	}
	k.namespace = nms.Items[s].GetName()
	k.fsm.Change("countPods")
	return nil
}

func (k *Kubetrbl) countPods() error {
	pods, err := k.k8sClient.CoreV1().Pods(k.namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	fmt.Printf("There are %d pods in the cluster+namespace.\n", len(pods.Items))
	k.pods = pods
	k.fsm.Change("checkPendingPods")
	return nil
}

func (k *Kubetrbl) checkPendingPods() error {
	for _, pod := range k.pods.Items {
		fmt.Println(pod.GetName())
		status := pod.Status
		if status.Phase == corev1.PodPending {
			fmt.Println("### Pending - " + pod.GetName())
			// TODO transition to pending checks
			return nil
		}
	}
	fmt.Println("No pods are pending.")
	k.fsm.Change("checkRunningPods")
	return nil
}

func (k *Kubetrbl) checkRunningPods() error {
	for _, pod := range k.pods.Items {
		status := pod.Status
		if status.Phase != corev1.PodRunning {
			fmt.Println("### Not running - " + pod.GetName())
			// TODO transition to not running checks
			return nil
		}
	}
	fmt.Println("All pods are running.")
	k.fsm.Change("checkReadyPods")
	return nil
}

func (k *Kubetrbl) checkReadyPods() error {
	for _, pod := range k.pods.Items {
		status := pod.Status
		for _, c := range status.Conditions {
			if c.Type == corev1.PodReady && c.Status != corev1.ConditionTrue {
				fmt.Println("### Not ready - " + pod.GetName())
				// TODO transition to not ready checks
				return nil
			}
		}
	}
	fmt.Println("All pods are ready.")
	k.fsm.Change("getPodName")
	return nil
}

func (k *Kubetrbl) getPodName() error {
	fmt.Println("Which pod are we investigating? ")
	podName, err := k.readString()
	if err != nil {
		return err
	}

	results := []corev1.Pod{}
	for _, pod := range k.pods.Items {
		if strings.Contains(pod.GetName(), podName) {
			results = append(results, pod)
		}
	}

	if len(results) == 0 {
		return errors.New("no results for search, try again")
	} else if len(results) > 1 {
		return errors.New("too many results, " + strconv.Itoa(len(results)) + ", try again.")
	}

	fmt.Println("Found pod...")
	k.pod = results[0]
	k.fsm.Change("getPort")
	return nil
}

func (k *Kubetrbl) getPort() error {
	portNum := 0
	result := []corev1.ContainerPort{}
	for _, cnt := range k.pod.Spec.Containers {
		for _, port := range cnt.Ports {
			fmt.Println(strconv.Itoa(portNum) + ") " + port.Name + ":" + strconv.Itoa(int(port.ContainerPort)))
			result = append(result, port)
			portNum++
		}
	}
	fmt.Println("Select the port: ")
	answer, err := k.readString()
	if err != nil {
		return err
	}

	selection, err := strconv.Atoi(answer)
	if err != nil {
		return err
	}

	p := result[selection]

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

	client, err := rest.RESTClientFor(k.config)
	if err != nil {
		return err
	}
	req := client.Post().
		Resource("pods").
		Namespace(k.namespace).
		Name(k.pod.Name).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(k.config)
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())
	stopChan := make(chan struct{}, 1)
	readyChan := make(chan struct{})
	pf, err := portforward.New(
		dialer,
		[]string{fmt.Sprintf("%d:%d", 8080, p.ContainerPort)}, // TODO retrieve local port from user
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
	fmt.Println(resp.Status)
	fmt.Println(resp.Header)

	close(stopChan)

	return nil
}

func (k *Kubetrbl) readString() (string, error) {
	str, err := k.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(str), nil
}
