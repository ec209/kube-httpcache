package controller

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"text/template"

	"github.com/golang/glog"
	varnishclient "github.com/martin-helmich/go-varnish-client"
)

func (v *VarnishController) watchConfigUpdates(ctx context.Context, c *exec.Cmd, errors chan<- error) {
	i := 0

	for {
		i++

		select {
		case tmplContents := <-v.vclTemplateUpdates: // templateupdates channel got a new item
			glog.Infof("VCL template was updated")

			tmpl, err := template.New("vcl").Parse(string(tmplContents)) // make a new VCL out of the item
			if err != nil {
				errors <- err
				continue
			}

			v.vclTemplate = tmpl // assign that to varnishController struct

			errors <- v.rebuildConfig(ctx, i)

		case newConfig := <-v.frontendUpdates: // frontend channel got a new item
			glog.Infof("received new frontend configuration: %+v", newConfig)

			v.frontend = newConfig // update the frontend of varnishController

			if v.varnishSignaller != nil {
				v.varnishSignaller.SetEndpoints(v.frontend) // update the frontend in the signaller object
			}

			errors <- v.rebuildConfig(ctx, i)

		case newConfig := <-v.backendUpdates: // backend channel got a new item
			glog.Infof("received new backend configuration: %+v", newConfig)

			v.backend = newConfig // update the backend of varnishController

			errors <- v.rebuildConfig(ctx, i) // basically rebuild varnishController with an updated backend

		case <-ctx.Done():
			errors <- ctx.Err()
			return
		}
	}
}

func (v *VarnishController) rebuildConfig(ctx context.Context, i int) error {
	buf := new(bytes.Buffer)

	err := v.renderVCL(buf, v.frontend.Endpoints, v.frontend.Primary, v.backend.Endpoints, v.backend.Primary)
	if err != nil {
		return err
	}

	vcl := buf.Bytes()
	glog.V(8).Infof("new VCL: %s", string(vcl))

	client, err := varnishclient.DialTCP(ctx, fmt.Sprintf("127.0.0.1:%d", v.AdminPort))
	if err != nil {
		return err
	}

	err = client.Authenticate(ctx, v.secret)
	if err != nil {
		return err
	}

	configname := fmt.Sprintf("k8s-upstreamcfg-%d", i)

	err = client.DefineInlineVCL(ctx, configname, vcl, varnishclient.VCLStateAuto) // DefineInlineVCL compiles and loads a new VCL file with the file contents
	if err != nil {
		return err
	}

	err = client.UseVCL(ctx, configname) // UseVCL make Varnish switch to the specified configuration file immediately
	if err != nil {
		return err
	}

	if v.currentVCLName == "" {
		v.currentVCLName = "boot"
	}

	//SetVCLState can be used to force a loaded VCL file to a specific state. not sure what VCL cold state, but looks to change the state
	if err := client.SetVCLState(ctx, v.currentVCLName, varnishclient.VCLStateCold); err != nil {
		glog.V(1).Infof("error while changing state of VCL %s: %s", v.currentVCLName, err)
	}

	v.currentVCLName = configname

	return nil
}
