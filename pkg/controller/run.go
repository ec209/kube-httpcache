package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/golang/glog"
	"github.com/mittwald/kube-httpcache/pkg/watcher"
)

func (v *VarnishController) Run(ctx context.Context) error {
	glog.Infof("waiting for initial configuration before starting Varnish")

	v.frontend = watcher.NewEndpointConfig() // assign an empty endpoint to frontend of varnish controller
	if v.frontendUpdates != nil {            // the watcher function keep it's eye on endpoint channel, if there is a change
		v.frontend = <-v.frontendUpdates // update the frontend with the item from the channel
		if v.varnishSignaller != nil {
			v.varnishSignaller.SetEndpoints(v.frontend) // update signaller's endpoint
		}
	}

	v.backend = watcher.NewEndpointConfig()
	if v.backendUpdates != nil {
		v.backend = <-v.backendUpdates // update backend endconfig
	}

	target, err := os.Create(v.configFile) // bring up configFile
	if err != nil {
		return err
	}

	glog.Infof("creating initial VCL config")
	// Write Endpoints, Primary Endpoint, backend_endpoints, and Primary Backend_endpoint to target
	err = v.renderVCL(target, v.frontend.Endpoints, v.frontend.Primary, v.backend.Endpoints, v.backend.Primary)
	if err != nil {
		return err
	}

	cmd, errChan := v.startVarnish(ctx)

	if err := v.waitForAdminPort(ctx); err != nil {
		return err
	}

	watchErrors := make(chan error)
	go v.watchConfigUpdates(ctx, cmd, watchErrors) // this go routine watches for the frontend/backend/template update or error our if the ctx got cancelled

	// This go routine basically prints out the logs from the go routine above
	go func() {
		for err := range watchErrors {
			if err != nil {
				glog.Warningf("error while watching for updates: %s", err.Error())
			}
		}
	}()

	return <-errChan // channel about error from varnishd cmd
}

func (v *VarnishController) startVarnish(ctx context.Context) (*exec.Cmd, <-chan error) {
	// inject certain cmd to the context
	c := exec.CommandContext(
		ctx,
		"varnishd",
		v.generateArgs()..., // This ... prolly refers that there are multiple outputs
	)

	// default dir
	c.Dir = "/"
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	r := make(chan error)

	// this go routine actually runs Commands defined in CommandContext
	go func() {
		err := c.Run()
		r <- err
	}()

	return c, r
}

func (v *VarnishController) generateArgs() []string {
	args := []string{
		"-F",
		"-f", v.configFile,
		"-S", v.SecretFile,
		"-s", v.Storage,
		"-a", fmt.Sprintf("%s:%d", v.FrontendAddr, v.FrontendPort),
		"-T", fmt.Sprintf("%s:%d", v.AdminAddr, v.AdminPort),
	}

	if v.AdditionalParameters != "" {
		for _, val := range strings.Split(v.AdditionalParameters, ",") {
			args = append(args, "-p")
			args = append(args, val)
		}
	}

	if v.WorkingDir != "" {
		args = append(args, "-n", v.WorkingDir)
	}

	return args
}
