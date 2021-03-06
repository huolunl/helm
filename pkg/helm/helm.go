/*
Copyright The Helm Authors.

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

package helm // import "github.com/huolunl/helm/v3/pkg/helm"

import (
	"bytes"
	"fmt"
	"github.com/huolunl/helm/v3/pkg/diff"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	// Import to initialize client auth plugins.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/huolunl/helm/v3/pkg/action"
	"github.com/huolunl/helm/v3/pkg/cli"
	"github.com/huolunl/helm/v3/pkg/gates"
	"github.com/huolunl/helm/v3/pkg/kube"
	kubefake "github.com/huolunl/helm/v3/pkg/kube/fake"
	"github.com/huolunl/helm/v3/pkg/release"
	"github.com/huolunl/helm/v3/pkg/storage/driver"
)

// FeatureGateOCI is the feature gate for checking if `helm chart` and `helm registry` commands should work
const FeatureGateOCI = gates.Gate("HELM_EXPERIMENTAL_OCI")

var settings = cli.New()

func init() {
	log.SetFlags(log.Lshortfile)
	diff.Register(Exec)
}

func debug(format string, v ...interface{}) {
	if settings.Debug {
		format = fmt.Sprintf("[debug] %s\n", format)
		log.Output(2, fmt.Sprintf(format, v...))
	}
}

func warning(format string, v ...interface{}) {
	format = fmt.Sprintf("WARNING: %s\n", format)
	fmt.Fprintf(os.Stderr, format, v...)
}

func main() {
	// Setting the name of the app for managedFields in the Kubernetes client.
	// It is set here to the full name of "helm" so that renaming of helm to
	// another name (e.g., helm2 or helm3) does not change the name of the
	// manager as picked up by the automated name detection.
	kube.ManagedFieldsManager = "helm"

	actionConfig := new(action.Configuration)
	cmd, err := newRootCmd(actionConfig, os.Stdout, os.Args[1:])
	if err != nil {
		warning("%+v", err)
		log.Println(1)
	}

	// run when each command's execute method is called
	cobra.OnInitialize(func() {
		helmDriver := os.Getenv("HELM_DRIVER")
		if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), helmDriver, debug); err != nil {
			log.Println(err)
		}
		if helmDriver == "memory" {
			loadReleasesInMemory(actionConfig)
		}
	})

	if err := cmd.Execute(); err != nil {
		debug("%+v", err)
		switch e := err.(type) {
		case PluginError:
			log.Println(e.Code)
		default:
			log.Println(1)
		}
	}
}

func Exec(isDiff bool, args ...string) ([]byte, error) {
	os.Args = append([]string{"helm"}, args...)
	var writer = bytes.Buffer{}
	kube.ManagedFieldsManager = "helm"

	actionConfig := new(action.Configuration)
	cmd, err := newRootCmd(actionConfig, &writer, os.Args[1:])
	if err != nil {
		return nil, err
	}
	// run when each command's execute method is called
	cobra.OnInitialize(func() {
		helmDriver := os.Getenv("HELM_DRIVER")
		if err := actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), helmDriver, debug); err != nil {
			log.Println(err)
		}
		if helmDriver == "memory" {
			loadReleasesInMemory(actionConfig)
		}
	})

	if err := cmd.Execute(); err != nil {
		debug("%+v", err)
		switch e := err.(type) {
		case PluginError:
			log.Printf("helm plugin error,%v", e)
		default:
			log.Printf("helm error,%v", e)
		}
		return writer.Bytes(), err
	}
	if isDiff {
		writer.WriteString("No changes detected,skipped update\n")
	}
	return writer.Bytes(), err
}

func checkOCIFeatureGate() func(_ *cobra.Command, _ []string) error {
	return func(_ *cobra.Command, _ []string) error {
		if !FeatureGateOCI.IsEnabled() {
			return FeatureGateOCI.Error()
		}
		return nil
	}
}

// This function loads releases into the memory storage if the
// environment variable is properly set.
func loadReleasesInMemory(actionConfig *action.Configuration) {
	filePaths := strings.Split(os.Getenv("HELM_MEMORY_DRIVER_DATA"), ":")
	if len(filePaths) == 0 {
		return
	}

	store := actionConfig.Releases
	mem, ok := store.Driver.(*driver.Memory)
	if !ok {
		// For an unexpected reason we are not dealing with the memory storage driver.
		return
	}

	actionConfig.KubeClient = &kubefake.PrintingKubeClient{Out: ioutil.Discard}

	for _, path := range filePaths {
		b, err := ioutil.ReadFile(path)
		if err != nil {
			log.Println("Unable to read memory driver data", err)
		}

		releases := []*release.Release{}
		if err := yaml.Unmarshal(b, &releases); err != nil {
			log.Println("Unable to unmarshal memory driver data: ", err)
		}

		for _, rel := range releases {
			if err := store.Create(rel); err != nil {
				log.Println(err)
			}
		}
	}
	// Must reset namespace to the proper one
	mem.SetNamespace(settings.Namespace())
}
