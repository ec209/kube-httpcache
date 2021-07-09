package controller

import (
	"context"
	"fmt"
	"github.com/golang/glog"
	"github.com/martin-helmich/go-varnish-client"
	"time"
)

func (v *VarnishController) waitForAdminPort(ctx context.Context) error {
	glog.Infof("probing admin port until it is available")
	addr := fmt.Sprintf("127.0.0.1:%d", v.AdminPort) // addr here would be an address:port in string

	t := time.NewTicker(time.Second) // timer 1 second
	defer t.Stop()                   // defer function to make sure to close the timer channel

	for {
		select {
		case <-t.C: // basically blocking for a seoncd
			_, err := varnishclient.DialTCP(ctx, addr) // DialTCP connects to an existing Varnish administration port.
			// This method does not perform authentication. Use the `Authenticate()` method for that.
			if err == nil {
				glog.Infof("admin port is available")
				return nil
			}

			glog.V(6).Infof("admin port is not available yet. waiting")
		case <-ctx.Done(): // request cancel
			return ctx.Err() // Throw error to context
		}
	}
}
