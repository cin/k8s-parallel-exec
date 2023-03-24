package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

type ColorCode int

const (
	BrightRed ColorCode = iota + 91
	BrightGreen
	BrightYellow
	BrightBlue
	BrightMagenta
	BrightCyan
	BrightWhite
)

const (
	divText      = "----------------------------------------------------\n"
	divColor     = BrightYellow
	podNameColor = BrightCyan
)

type PodResult struct {
	podName string
	output  string
	err     error
}

type ByPodName []PodResult

func (p ByPodName) Len() int           { return len(p) }
func (p ByPodName) Less(i, j int) bool { return p[i].podName < p[j].podName }
func (p ByPodName) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to the kubeconfig file")
	containerFlag := flag.String("c", "", "Container to execute the command against")
	labelFlag := flag.String("l", "", "Label selector to filter pods")
	flag.Parse()

	if *containerFlag == "" {
		fmt.Println("Error: container name must be specified with -c")
		os.Exit(1)
	}

	if *labelFlag == "" {
		fmt.Println("Error: label selector must be specified with -l")
		os.Exit(1)
	}

	args := flag.Args()
	if len(args) == 0 {
		fmt.Println("Error: command to execute is required")
		os.Exit(1)
	}

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		config, err = rest.InClusterConfig()
		if err != nil {
			panic(err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	pods, err := clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{
		LabelSelector: *labelFlag,
	})
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup
	resultsChan := make(chan PodResult, len(pods.Items))

	for _, pod := range pods.Items {
		wg.Add(1)
		go func(p v1.Pod) {
			defer wg.Done()
			resultsChan <- execCommand(config, clientset, p, *containerFlag, args)
		}(pod)
	}

	wg.Wait()
	close(resultsChan)

	var results []PodResult
	for result := range resultsChan {
		results = append(results, result)
	}

	sort.Sort(ByPodName(results))

	for _, result := range results {
		if result.err != nil {
			fmt.Printf("Error executing command on pod %s: %v\n%s", result.podName, err, result.output)
		} else {
			fmt.Printf("%sPod %s\n%s%s",
				colorize(divColor, divText),
				colorize(podNameColor, result.podName),
				colorize(divColor, divText),
				result.output)
		}
	}
}

func colorize(colorCode ColorCode, text string) string {
	return fmt.Sprintf("\033[%dm%s\033[0m", colorCode, text)
}

func execCommand(config *rest.Config, clientset *kubernetes.Clientset, pod v1.Pod, container string, command []string) PodResult {
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return PodResult{pod.Name, "", err}
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdout: bufio.NewWriter(&stdout),
		Stderr: bufio.NewWriter(&stderr),
	})

	if err != nil {
		return PodResult{pod.Name, "", err}
	}

	if stderr.Len() > 0 {
		return PodResult{pod.Name, stdout.String(), fmt.Errorf("stderr: %s", stderr.String())}
	}

	return PodResult{pod.Name, stdout.String(), nil}
}
