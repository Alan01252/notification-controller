/*
Copyright 2022 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package notifier

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/url"
	"strings"

	"github.com/fluxcd/pkg/runtime/events"
	"github.com/pkg/errors"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v2"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
)

type Alertmanager struct {
	URL            string
	ProxyURL       string
	CertPool       *x509.CertPool
	RelabelConfig  *relabel.Config
	PerformRelabel bool
}

type AlertManagerAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

func NewAlertmanager(hookURL string, proxyURL string, certPool *x509.CertPool, relabelConfig string) (*Alertmanager, error) {

	urls := strings.Split(hookURL, ",")
	var wrapErr error
	for _, alertmanagerUrl := range urls {
		_, err := url.ParseRequestURI(alertmanagerUrl)
		if err != nil {
			wrapErr = errors.Wrap(wrapErr, err.Error())
		}
	}
	if wrapErr != nil {
		return nil, wrapErr
	}

	performRelabel := false
	config := &relabel.Config{}
	if len(relabelConfig) > 0 {
		err := yaml.UnmarshalStrict([]byte(relabelConfig), &config)
		if err != nil {
			return nil, err
		}
		performRelabel = true
	}

	return &Alertmanager{
		URL:            hookURL,
		ProxyURL:       proxyURL,
		CertPool:       certPool,
		RelabelConfig:  config,
		PerformRelabel: performRelabel,
	}, nil
}

func (s *Alertmanager) Post(ctx context.Context, event events.Event) error {
	// Skip any update events
	if isCommitStatus(event.Metadata, "update") {
		return nil
	}

	annotations := make(map[string]string)
	annotations["message"] = event.Message

	_, ok := event.Metadata["summary"]
	if ok {
		annotations["summary"] = event.Metadata["summary"]
		delete(event.Metadata, "summary")
	}

	var alertLabels = make(map[string]string)
	if event.Metadata != nil {
		alertLabels = event.Metadata
	}
	alertLabels["alertname"] = "Flux" + event.InvolvedObject.Kind + cases.Title(language.English, cases.NoLower).String(event.Reason)
	alertLabels["severity"] = event.Severity
	alertLabels["reason"] = event.Reason
	alertLabels["timestamp"] = event.Timestamp.String()

	alertLabels["kind"] = event.InvolvedObject.Kind
	alertLabels["name"] = event.InvolvedObject.Name
	alertLabels["namespace"] = event.InvolvedObject.Namespace
	alertLabels["reportingcontroller"] = event.ReportingController

	promLabels := labels.FromMap(alertLabels)
	if s.PerformRelabel {
		res := relabel.Process(promLabels, s.RelabelConfig)
		alertLabels = res.Map()
	}

	payload := []AlertManagerAlert{
		{
			Labels:      alertLabels,
			Annotations: annotations,
			Status:      "firing",
		},
	}

	urls := strings.Split(s.URL, ",")
	var wrapErr error
	for _, url := range urls {
		err := postMessage(url, s.ProxyURL, s.CertPool, payload)
		if err != nil {
			wrapErr = errors.Wrap(wrapErr, err.Error())
		}

	}

	if wrapErr != nil {
		return fmt.Errorf("postMessage failed: %w", wrapErr)
	}

	return nil
}
