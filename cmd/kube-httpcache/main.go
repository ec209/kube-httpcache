package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/golang/glog"
	"github.com/mittwald/kube-httpcache/cmd/kube-httpcache/internal"
	"github.com/mittwald/kube-httpcache/pkg/controller"
	"github.com/mittwald/kube-httpcache/pkg/signaller"
	"github.com/mittwald/kube-httpcache/pkg/watcher"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var opts internal.KubeHTTPProxyFlags

func init() {
	flag.Set("logtostderr", "true") // enable sending log to stderr
}

func main() {
	if err := opts.Parse(); err != nil { // setting up startup value
		panic(err) // abort the main prcoess if error
	}

	glog.Infof("running kube-httpcache with following options: %+v", opts)

	var config *rest.Config // Config holds the common attributes that can be passed to a Kubernetes client on initialization.
	var err error
	var client kubernetes.Interface

	if opts.Kubernetes.Config == "" {
		glog.Infof("using in-cluster configuration")
		config, err = rest.InClusterConfig() // ServiceAccount of pod
	} else {
		glog.Infof("using configuration from '%s'", opts.Kubernetes.Config)
		config, err = clientcmd.BuildConfigFromFlags("", opts.Kubernetes.Config) // build kube config outta opts (pre-defined)
	}

	if err != nil {
		panic(err)
	}

	client = kubernetes.NewForConfigOrDie(config) // creates a new Clientset for the given config, Clientset contains the clients for groups

	var frontendUpdates chan *watcher.EndpointConfig
	var frontendErrors chan error
	if opts.Frontend.Watch { // if the frontend watch is already initiated
		frontendWatcher := watcher.NewEndpointWatcher( // Create new Frontend endpoint watcher
			client,
			opts.Frontend.Namespace,
			opts.Frontend.Service,
			opts.Frontend.PortName,
			opts.Kubernetes.RetryBackoff,
		)
		frontendUpdates, frontendErrors = frontendWatcher.Run() // init watch loop, send the signal to channels
	}

	var backendUpdates chan *watcher.EndpointConfig
	var backendErrors chan error
	if opts.Backend.Watch {
		backendWatcher := watcher.NewEndpointWatcher(
			client,
			opts.Backend.Namespace,
			opts.Backend.Service,
			opts.Backend.PortName,
			opts.Kubernetes.RetryBackoff,
		)
		backendUpdates, backendErrors = backendWatcher.Run()
	}

	templateWatcher := watcher.MustNewTemplateWatcher(opts.Varnish.VCLTemplate, opts.Varnish.VCLTemplatePoll) // if polling is true, pulls the new vcl config
	templateUpdates, templateErrors := templateWatcher.Run()                                                  // init watch loop

	var varnishSignaller *signaller.Signaller // signaller is basically a module to handle purge and ban command of varnish
	var varnishSignallerErrors chan error
	if opts.Signaller.Enable {
		varnishSignaller = signaller.NewSignaller(
			opts.Signaller.Address,
			opts.Signaller.Port,
			opts.Signaller.WorkersCount,
			opts.Signaller.MaxRetries,
			opts.Signaller.RetryBackoff,
		)
		varnishSignallerErrors = varnishSignaller.GetErrors()

		go func() { // Not sure why is it running as a go routine here
			err = varnishSignaller.Run()
			if err != nil {
				panic(err)
			}
		}()
	}

	go func() {
		for {
			select { // fan in pattern, if error pops up from any of process, put it in a stderr
			case err := <-frontendErrors:
				glog.Errorf("error while watching frontends: %s", err.Error())
			case err := <-backendErrors:
				glog.Errorf("error while watching backends: %s", err.Error())
			case err := <-templateErrors:
				glog.Errorf("error while watching template changes: %s", err.Error())
			case err := <-varnishSignallerErrors:
				glog.Errorf("error while running varnish signaller: %s", err.Error())
			}
		}
	}()

	// init controller object
	varnishController, err := controller.NewVarnishController(
		opts.Varnish.SecretFile,
		opts.Varnish.Storage,
		opts.Varnish.AdditionalParameters,
		opts.Varnish.WorkingDir,
		opts.Frontend.Address,
		opts.Frontend.Port,
		opts.Admin.Address,
		opts.Admin.Port,
		frontendUpdates,
		backendUpdates,
		templateUpdates,
		varnishSignaller,
		opts.Varnish.VCLTemplate,
	)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background()) // WithCancel returns a copy of parent with a new Done channel. The returned context's Done channel is closed when the returned cancel function is called or when the parent context's Done channel is closed, whichever happens first.

	signals := make(chan os.Signal, 1) // channel to get os level signal(like SIGTERM) with buffer size 1

	signal.Notify(signals, syscall.SIGINT) // signal.Notify registers the given channel to receive notifications of the specified signals
	signal.Notify(signals, syscall.SIGTERM)

	go func() {
		s := <-signals

		glog.Infof("received signal %s", s) // whenever the channel get the os signal, goroutine prints out which signal it got to stdout
		cancel()
	}()

	err = varnishController.Run(ctx) // update the context whenever regarding new temp/frontend/backend update
	if err != nil {
		panic(err)
	}
}
