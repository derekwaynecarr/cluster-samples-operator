package e2e_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	kapis "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-sdk/pkg/sdk"

	configv1 "github.com/openshift/api/config/v1"
	imageapiv1 "github.com/openshift/api/image/v1"
	operatorsv1api "github.com/openshift/api/operator/v1"
	templatev1 "github.com/openshift/api/template/v1"
	samplesapi "github.com/openshift/cluster-samples-operator/pkg/apis/samplesoperator/v1alpha1"
	"github.com/openshift/cluster-samples-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-samples-operator/pkg/stub"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	imagestreamsKey = "imagestreams"
	templatesKey    = "templates"
)

func dumpPod(t *testing.T) {
	kubeConfigFile := os.Getenv("KUBERNETES_CONFIG")
	if len(kubeConfigFile) == 0 {
		t.Fatalf("KUBERNETES_CONFIG needs to be set")
	}
	kubeConfigFile = os.Getenv("KUBECONFIG")
	if len(kubeConfigFile) == 0 {
		t.Fatalf("KUBECONFIG needs to be set")
	}
	restClient, err := clientcmd.BuildConfigFromFlags("", kubeConfigFile)
	if err != nil {
		t.Fatalf("error getting rest client %v", err)
	}
	coreClient, err := corev1client.NewForConfig(restClient)
	if err != nil {
		t.Fatalf("error getting core client %v", err)
	}
	podClient := coreClient.Pods("openshift-cluster-samples-operator")
	podList, err := podClient.List(metav1.ListOptions{})
	if err != nil {
		t.Fatalf("error list pods %v", err)
	}
	for _, pod := range podList.Items {
		if strings.HasPrefix(pod.Name, "cluster-samples-operator") {
			req := podClient.GetLogs(pod.Name, &corev1.PodLogOptions{})
			readCloser, err := req.Stream()
			if err != nil {
				t.Fatalf("error getting pod logs %v", err)
			}
			b, err := ioutil.ReadAll(readCloser)
			if err != nil {
				t.Fatalf("error reading pod stream %v", err)
			}
			podLog := string(b)
			t.Logf("pod logs:  %s", podLog)
		}
	}
}

func verifyOperatorUp(t *testing.T) *samplesapi.SamplesResource {
	sr := &samplesapi.SamplesResource{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SamplesResource",
			APIVersion: samplesapi.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-samples",
			Namespace: "openshift-cluster-samples-operator",
		},
	}
	err := wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		if err := sdk.Get(sr); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("error waiting for samples resource to appear: %v", err)
	}
	return sr
}

func verifyConditionsCompleteSamplesAdded(sr *samplesapi.SamplesResource) error {
	return wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		if err := sdk.Get(sr); err != nil {
			return false, nil
		}
		if sr.Condition(samplesapi.SamplesExist).Status != corev1.ConditionTrue ||
			sr.Condition(samplesapi.ImageChangesInProgress).Status != corev1.ConditionFalse {
			return false, nil
		}

		return true, nil
	})

}

func verifyConditionsCompleteSamplesRemoved(sr *samplesapi.SamplesResource) error {
	return wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		if err := sdk.Get(sr); err != nil {
			return false, nil
		}
		if sr.Condition(samplesapi.SamplesExist).Status != corev1.ConditionFalse ||
			sr.Condition(samplesapi.ImageChangesInProgress).Status != corev1.ConditionFalse {
			return false, nil
		}

		return true, nil
	})
}

func verifyClusterOperatorConditionsComplete(t *testing.T) {
	state := &configv1.ClusterOperator{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "ClusterOperator",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: operator.ClusterOperatorName,
		},
	}
	err := wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		if err := sdk.Get(state); err != nil {
			return false, nil
		}
		availableOK := false
		progressingOK := false
		failingOK := false
		for _, condition := range state.Status.Conditions {
			switch condition.Type {
			case configv1.OperatorAvailable:
				if condition.Status == configv1.ConditionTrue {
					availableOK = true
				}
			case configv1.OperatorFailing:
				if condition.Status == configv1.ConditionFalse {
					failingOK = true
				}
			case configv1.OperatorProgressing:
				if condition.Status == configv1.ConditionFalse {
					progressingOK = true
				}
			}
		}
		if availableOK && progressingOK && failingOK {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		dumpPod(t)
		sr := verifyOperatorUp(t)
		t.Fatalf("cluster operator conditions never stabilized, cluster op %#v samples resource %#v", state, sr)
	}
}

func getContentDir(t *testing.T) string {
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("%v", err)
	}
	startDir := filepath.Dir(pwd)
	for true {
		// filepath.Dir will either return . or / it expires path,
		// just go off of len given os.IsPathSeprator is uint8 and
		// conversion from string to uint8 is cumbersome
		if len(startDir) <= 1 {
			break
		}
		if strings.HasSuffix(strings.TrimSpace(startDir), "cluster-samples-operator") {
			break
		}
		startDir = filepath.Dir(startDir)
	}
	contentDir := ""
	_ = filepath.Walk(startDir, func(path string, info os.FileInfo, err error) error {
		if strings.HasSuffix(strings.TrimSpace(path), "okd-x86_64") {
			contentDir = path
			return fmt.Errorf("found contentDir %s", contentDir)
		}
		return nil
	})
	return contentDir
}

func getSamplesNames(dir string, files []os.FileInfo, t *testing.T) map[string]map[string]bool {

	h := stub.Handler{}
	h.Fileimagegetter = &stub.DefaultImageStreamFromFileGetter{}
	h.Filetemplategetter = &stub.DefaultTemplateFromFileGetter{}
	h.Filefinder = &stub.DefaultResourceFileLister{}

	var err error
	if files == nil {
		files, err = h.Filefinder.List(dir)
	}
	if err != nil {
		t.Fatalf("%v", err)
	}

	names := map[string]map[string]bool{}
	names[imagestreamsKey] = map[string]bool{}
	names[templatesKey] = map[string]bool{}
	for _, file := range files {
		if file.IsDir() {
			subfiles, err := h.Filefinder.List(dir + "/" + file.Name())
			if err != nil {
				t.Fatalf("%v", err)
			}
			subnames := getSamplesNames(dir+"/"+file.Name(), subfiles, t)
			substreams, _ := subnames[imagestreamsKey]
			subtemplates, _ := subnames[templatesKey]
			streams, _ := names[imagestreamsKey]
			templates, _ := names[templatesKey]

			if len(streams) == 0 {
				streams = substreams
			} else {
				for key, value := range substreams {
					streams[key] = value
				}
			}
			if len(templates) == 0 {
				templates = subtemplates
			} else {
				for key, value := range subtemplates {
					templates[key] = value
				}
			}

			names[imagestreamsKey] = streams
			names[templatesKey] = templates

			continue
		}

		if strings.HasSuffix(dir, imagestreamsKey) {
			imagestream, err := h.Fileimagegetter.Get(dir + "/" + file.Name())
			if err != nil {
				t.Fatalf("%v", err)
			}

			streams, _ := names[imagestreamsKey]
			streams[imagestream.Name] = true
		}

		if strings.HasSuffix(dir, templatesKey) {
			template, err := h.Filetemplategetter.Get(dir + "/" + file.Name())
			if err != nil {
				t.Fatalf("%v", err)
			}

			templates, _ := names[templatesKey]
			templates[template.Name] = true
		}
	}
	return names
}

func verifyImageStreamsPresent(t *testing.T, content map[string]bool, timeToCompare *kapis.Time) {
	for key, _ := range content {
		is := &imageapiv1.ImageStream{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ImageStream",
				APIVersion: imageapiv1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      key,
				Namespace: "openshift",
			},
		}

		err := wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
			if err := sdk.Get(is); err != nil {
				t.Logf("%v", err)
				return false, nil
			}
			if timeToCompare != nil && is.CreationTimestamp.Before(timeToCompare) {
				errstr := fmt.Sprintf("imagestream %s was created at %#v which is still created before time to compare %#v", is.Name, is.CreationTimestamp, timeToCompare)
				t.Log(errstr)
				return false, fmt.Errorf(errstr)
			}
			return true, nil
		})
		if err != nil {
			dumpPod(t)
			sr := verifyOperatorUp(t)
			t.Fatalf("error waiting for example imagestream %s to appear: %v samples resource %#v", key, err, sr)
		}
	}
}

func verifyTemplatesPresent(t *testing.T, content map[string]bool, timeToCompare *kapis.Time) {
	for key, _ := range content {
		template := &templatev1.Template{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Template",
				APIVersion: templatev1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      key,
				Namespace: "openshift",
			},
		}

		err := wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
			if err := sdk.Get(template); err != nil {
				t.Logf("%v", err)
				return false, nil
			}
			if timeToCompare != nil && template.CreationTimestamp.Before(timeToCompare) {
				errstr := fmt.Sprintf("template %s was created at %#v which is still created before time to compare %#v", template.Name, template.CreationTimestamp, timeToCompare)
				t.Log(errstr)
				return false, fmt.Errorf(errstr)
			}
			return true, nil
		})
		if err != nil {
			dumpPod(t)
			sr := verifyOperatorUp(t)
			t.Fatalf("error waiting for example template %s to appear: %v samples resource %#v", key, err, sr)
		}
	}
}

func validateContent(t *testing.T, timeToCompare *kapis.Time) {
	contentDir := getContentDir(t)
	content := getSamplesNames(contentDir, nil, t)
	streams, _ := content[imagestreamsKey]
	verifyImageStreamsPresent(t, streams, timeToCompare)
	templates, _ := content[templatesKey]
	verifyTemplatesPresent(t, templates, timeToCompare)
}

func verifyConfigurationValid(t *testing.T, sr *samplesapi.SamplesResource, status corev1.ConditionStatus) {
	err := wait.PollImmediate(1*time.Second, 10*time.Second, func() (bool, error) {
		err := sdk.Get(sr)
		if err != nil {
			return false, err
		}
		if sr.Condition(samplesapi.ConfigurationValid).Status == status {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		dumpPod(t)
		sr := verifyOperatorUp(t)
		t.Fatalf("error waiting for samples resource to update config valid %v samplesresource %#v", err, sr)
	}
}

func verifyDeletedImageStreamRecreated(t *testing.T) {
	is := &imageapiv1.ImageStream{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageStream",
			APIVersion: imageapiv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jenkins",
			Namespace: "openshift",
		},
	}
	err := sdk.Delete(is, sdk.WithDeleteOptions(&metav1.DeleteOptions{}))
	if err != nil {
		dumpPod(t)
		sr := verifyOperatorUp(t)
		t.Fatalf("error deleting jenkins imagestream %v samplesresource %#v", err, sr)
	}
	err = wait.PollImmediate(1*time.Second, 30*time.Second, func() (bool, error) {
		err := sdk.Get(is)
		if err == nil {
			return true, nil
		}
		if kerrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	})
	if err != nil {
		t.Fatalf("imagestream not recreated: %v", err)
		dumpPod(t)
	}
}

func verifyDeletedImageStreamNotRecreated(t *testing.T) {
	is := &imageapiv1.ImageStream{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageStream",
			APIVersion: imageapiv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jenkins",
			Namespace: "openshift",
		},
	}
	err := sdk.Delete(is, sdk.WithDeleteOptions(&metav1.DeleteOptions{}))
	if err != nil {
		dumpPod(t)
		sr := verifyOperatorUp(t)
		t.Fatalf("error deleting jenkins imagestream %v samplesresource %#v", err, sr)
	}
	// make sure jenkins imagestream does not appear while unmanaged
	// first, wait sufficiently to make sure delete has gone though
	err = wait.PollImmediate(1*time.Second, 10*time.Second, func() (bool, error) {
		err := sdk.Get(is)
		if kerrors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		dumpPod(t)
		sr := verifyOperatorUp(t)
		t.Fatalf("delete did not occur %v samples resource %#v", err, sr)
	}
	// now make sure it has not been recreated
	time.Sleep(30 * time.Second)
	err = sdk.Get(is)
	if err == nil {
		t.Fatalf("imagestream recreated")
	}

}

func verifyDeletedTemplatesRecreated(t *testing.T) {
	temp := &templatev1.Template{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Template",
			APIVersion: templatev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jenkins-ephemeral",
			Namespace: "openshift",
		},
	}
	err := sdk.Delete(temp, sdk.WithDeleteOptions(&metav1.DeleteOptions{}))
	if err != nil {
		dumpPod(t)
		sr := verifyOperatorUp(t)
		t.Fatalf("error deleting jenkins imagestream %v samples resource %#v", err, sr)
	}
	err = wait.PollImmediate(1*time.Second, 30*time.Second, func() (bool, error) {
		err := sdk.Get(temp)
		if err == nil {
			return true, nil
		}
		if kerrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	})
	if err != nil {
		t.Fatalf("template not recreated: %v", err)
		dumpPod(t)
	}
}

func verifyDeletedTemplatesNotRecreated(t *testing.T) {
	temp := &templatev1.Template{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Template",
			APIVersion: templatev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jenkins-ephemeral",
			Namespace: "openshift",
		},
	}
	err := sdk.Delete(temp, sdk.WithDeleteOptions(&metav1.DeleteOptions{}))
	if err != nil {
		dumpPod(t)
		sr := verifyOperatorUp(t)
		t.Fatalf("error deleting jenkins imagestream %v samples resource %#v", err, sr)
	}
	// make sure jenkins-ephemeral template does not appear while unmanaged
	// first, wait sufficiently to make sure delete has gone though
	err = wait.PollImmediate(1*time.Second, 10*time.Second, func() (bool, error) {
		err := sdk.Get(temp)
		if kerrors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		dumpPod(t)
		sr := verifyOperatorUp(t)
		t.Fatalf("delete did not occur %v samples resource %#v", err, sr)
	}
	// now make sure it has not been recreated
	time.Sleep(30 * time.Second)
	err = sdk.Get(temp)
	if err == nil {
		dumpPod(t)
		sr := verifyOperatorUp(t)
		t.Fatalf("template recreated samples resource %#v", sr)
	}

}

func TestImageStreamInOpenshiftNamespace(t *testing.T) {
	verifyOperatorUp(t)
	validateContent(t, nil)
	verifyClusterOperatorConditionsComplete(t)
}

func TestRecreateSamplesResourceAfterDelete(t *testing.T) {
	sr := verifyOperatorUp(t)

	oldTime := sr.CreationTimestamp
	now := kapis.Now()

	err := sdk.Delete(sr, sdk.WithDeleteOptions(&metav1.DeleteOptions{}))
	if err != nil {
		dumpPod(t)
		t.Fatalf("error deleting samplesresource %v", err)
	}

	err = wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		if err := sdk.Get(sr); err != nil {
			return false, nil
		}
		if sr.CreationTimestamp == oldTime {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		dumpPod(t)
		t.Fatalf("creation times the same after delete: %v, %v, %#v", oldTime, sr.CreationTimestamp, sr)
	}

	err = verifyConditionsCompleteSamplesAdded(sr)
	if err != nil {
		dumpPod(t)
		sr = verifyOperatorUp(t)
		t.Fatalf("samples not re-established after delete %#v", sr)
	}

	validateContent(t, &now)
	verifyClusterOperatorConditionsComplete(t)
}

func TestSpecManagementStateField(t *testing.T) {
	sr := verifyOperatorUp(t)

	oldTime := sr.CreationTimestamp
	now := kapis.Now()
	sr.Spec.ManagementState = operatorsv1api.Removed
	err := sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samplesresource %v and %#v", err, sr)
	}

	err = verifyConditionsCompleteSamplesRemoved(sr)
	if err != nil {
		dumpPod(t)
		sr = verifyOperatorUp(t)
		t.Fatalf("samples not removed in time %#v", sr)
	}

	err = wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		if err := sdk.Get(sr); err != nil {
			return false, nil
		}
		if sr.CreationTimestamp != oldTime {
			return false, fmt.Errorf("SamplesResource was recreated when it should not have been: old create time %v new create time %v", oldTime, sr.CreationTimestamp)
		}
		return true, nil
	})
	if err != nil {
		dumpPod(t)
		sr = verifyOperatorUp(t)
		t.Fatalf("%v and %#v", err, sr)
	}

	isl := &imageapiv1.ImageStreamList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageStreamList",
			APIVersion: imageapiv1.SchemeGroupVersion.String(),
		},
	}
	sdk.List("openshift", isl, sdk.WithListOptions(&metav1.ListOptions{}))
	if len(isl.Items) > 0 {
		dumpPod(t)
		sr = verifyOperatorUp(t)
		t.Fatalf("still imagestreams in openshift namespace %#v samples resource %#v", isl.Items, sr)
	}
	tl := &templatev1.TemplateList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "TemplateList",
			APIVersion: templatev1.SchemeGroupVersion.String(),
		},
	}
	sdk.List("openshift", tl, sdk.WithListOptions(&metav1.ListOptions{}))
	if len(tl.Items) > 0 {
		dumpPod(t)
		sr = verifyOperatorUp(t)
		t.Fatalf("still templates in openshift namespace %#v samples resource %#v", tl.Items, sr)
	}

	sr = verifyOperatorUp(t)
	sr.Spec.ManagementState = operatorsv1api.Managed
	err = sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samplesresource %v and %#v", err, sr)
	}

	err = verifyConditionsCompleteSamplesAdded(sr)
	if err != nil {
		dumpPod(t)
		sr = verifyOperatorUp(t)
		t.Fatalf("samples not re-established when set to managed %#v", sr)
	}

	validateContent(t, &now)

	sr = verifyOperatorUp(t)
	sr.Spec.ManagementState = operatorsv1api.Unmanaged
	err = sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samplesresource %v", err)
	}

	verifyDeletedImageStreamNotRecreated(t)
	verifyDeletedTemplatesNotRecreated(t)
	// now switch back to default managed for any subsequent tests
	// and confirm all the default samples content exists
	sr = verifyOperatorUp(t)
	// get timestamp to check against in progress condition
	now = kapis.Now()
	sr.Spec.ManagementState = operatorsv1api.Managed
	err = sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samplesresource %v and %#v", err, sr)
	}

	// wait for it to get into pending
	err = wait.PollImmediate(1*time.Second, 30*time.Second, func() (bool, error) {
		err := sdk.Get(sr)
		if err != nil {
			return false, err
		}
		if sr.ConditionTrue(samplesapi.ImageChangesInProgress) {
			return true, nil
		}
		if sr.Condition(samplesapi.ImageChangesInProgress).LastUpdateTime.After(now.Time) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		dumpPod(t)
		t.Fatalf("error waiting for samplesresource to get into pending: %v samples resource %#v", err, sr)
	}

	// now wait for it to get out of pending
	err = verifyConditionsCompleteSamplesAdded(sr)
	if err != nil {
		dumpPod(t)
		sr = verifyOperatorUp(t)
		t.Fatalf("samples not re-established when set to managed %#v", sr)
	}

	validateContent(t, nil)
	verifyClusterOperatorConditionsComplete(t)
}

func TestInstallTypeConfigChangeValidation(t *testing.T) {
	sr := verifyOperatorUp(t)

	sr.Spec.InstallType = samplesapi.RHELSamplesDistribution
	err := sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samplesresource %v and %#v", err, sr)
	}

	verifyConfigurationValid(t, sr, corev1.ConditionFalse)

	//reset install type back
	sr = verifyOperatorUp(t)
	sr.Spec.InstallType = samplesapi.CentosSamplesDistribution
	err = sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samplesresource %v and %#v", err, sr)
	}

	verifyConfigurationValid(t, sr, corev1.ConditionTrue)
}

func TestArchitectureConfigChangeValidation(t *testing.T) {
	sr := verifyOperatorUp(t)

	sr.Spec.Architectures[0] = samplesapi.PPCArchitecture
	err := sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samplesresource %v and %#v", err, sr)
	}

	verifyConfigurationValid(t, sr, corev1.ConditionFalse)

	//reset install type back
	sr = verifyOperatorUp(t)
	sr.Spec.Architectures[0] = samplesapi.X86Architecture
	err = sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samplesresource %v and %#v", err, sr)
	}

	verifyConfigurationValid(t, sr, corev1.ConditionTrue)
}

func TestSkippedProcessing(t *testing.T) {
	sr := verifyOperatorUp(t)

	sr.Spec.SkippedImagestreams = append(sr.Spec.SkippedImagestreams, "jenkins")
	sr.Spec.SkippedTemplates = append(sr.Spec.SkippedTemplates, "jenkins-ephemeral")
	err := sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samples resource %v and %#v", err, sr)
	}

	verifyDeletedImageStreamNotRecreated(t)
	verifyDeletedTemplatesNotRecreated(t)

	// reset skipped list back
	sr = verifyOperatorUp(t)
	sr.Spec.SkippedImagestreams = []string{}
	sr.Spec.SkippedTemplates = []string{}
	err = sdk.Update(sr)
	if err != nil {
		dumpPod(t)
		t.Fatalf("error updating samplesresource %v and %#v", err, sr)
	}
	// checking in progress before validating content helps
	// isolate potential error causes
	sr = verifyOperatorUp(t)
	verifyConditionsCompleteSamplesAdded(sr)
	validateContent(t, nil)
	sr = verifyOperatorUp(t)
	verifyConditionsCompleteSamplesAdded(sr)
}

func TestRecreateDeletedManagedSample(t *testing.T) {
	verifyOperatorUp(t)
	verifyDeletedImageStreamRecreated(t)
	verifyDeletedTemplatesRecreated(t)
}
