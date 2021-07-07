package signaller

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"time"

	"github.com/golang/glog"
	"github.com/mittwald/kube-httpcache/pkg/watcher"
)

func (b *Signaller) Run() error {
	server := &http.Server{
		Addr:    b.Address + ":" + strconv.Itoa(b.Port),
		Handler: b, // Not sure How this Handler is working
	}

	for i := 0; i < b.WorkersCount; i++ {
		go b.ProcessSignalQueue() // goroutine making a request outta signal channel
	}

	return server.ListenAndServe() // listen from server
}

func (b *Signaller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		b.errors <- err
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	glog.V(5).Infof("received a signal request: %+v", r)

	b.mutex.RLock()
	endpoints := make([]watcher.Endpoint, len(b.endpoints.Endpoints))
	copy(endpoints, b.endpoints.Endpoints) // why is it copying endpoints?
	b.mutex.RUnlock()

	for _, endpoint := range endpoints {
		url := fmt.Sprintf("%s://%s:%s%s", b.EndpointScheme, endpoint.Host, endpoint.Port, r.RequestURI)
		request, err := http.NewRequest(r.Method, url, bytes.NewReader(body)) // Method eg (GET), URL of the endpoint. With those variables make a new http request
		if err != nil {
			b.errors <- err
		}
		//fill up http request as such and puth it in a channel
		request.Header = r.Header.Clone()
		request.Host = r.Host
		request.Header.Set("X-Forwarded-For", r.RemoteAddr)
		b.signalQueue <- Signal{request, 0}
	}

	fmt.Fprintf(w, "Signal request is being broadcasted.")
}

func (b *Signaller) ProcessSignalQueue() {
	client := &http.Client{}

	for signal := range b.signalQueue {
		response, err := client.Do(signal.Request) // Make a request and get a response
		if err != nil {
			glog.Errorf("singal broadcast error: %v", err.Error())
			glog.Infof("retring in %v", b.RetryBackoff)
			b.Retry(signal)
		} else if response.StatusCode >= 400 && response.StatusCode <= 599 {
			glog.Warningf("singal broadcast error: unusual status code from %s: %v", response.Request.URL.Host, response.Status)
			glog.Infof("retring in %v", b.RetryBackoff)
			b.Retry(signal)
		} else {
			glog.V(5).Infof("recieved a signal response from %s: %+v", response.Request.URL.Host, response)
		}

		// after reading all the response, still leftover? -> error
		if response != nil {
			if err := response.Body.Close(); err != nil {
				glog.Error("error on closing response body:", err)
			}
		}
	}
}

func (b *Signaller) Retry(signal Signal) {
	signal.Attempt++                   // add up the attempt number
	if signal.Attempt < b.MaxRetries { // as far as the attempt number is smaller than the maxretry number
		go func() {
			time.Sleep(b.RetryBackoff) // sleep it for retrybackoff duration
			b.signalQueue <- signal    // add a new signal into a channel
		}()
	}
}
