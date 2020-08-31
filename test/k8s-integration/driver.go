package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"k8s.io/klog"
	"k8s.io/kubernetes/test/e2e/framework/podlogs"
)

func getOverlayDir(pkgDir, deployOverlayName string) string {
	return filepath.Join(pkgDir, "deploy", "kubernetes", "overlays", deployOverlayName)
}

func installDriver(platform, goPath, pkgDir, stagingImage, stagingVersion, deployOverlayName string, doDriverBuild bool) error {
	if doDriverBuild {
		// Install kustomize
		klog.Infof("Installing kustomize")
		out, err := exec.Command(filepath.Join(pkgDir, "deploy", "kubernetes", "install-kustomize.sh")).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to install kustomize: %s, err: %v", out, err)
		}

		// Edit ci kustomization to use given image tag
		overlayDir := getOverlayDir(pkgDir, deployOverlayName)
		err = os.Chdir(overlayDir)
		if err != nil {
			return fmt.Errorf("failed to change to overlay directory: %s, err: %v", out, err)
		}

		// TODO (#138): in a local environment this is going to modify the actual kustomize files.
		// maybe a copy should be made instead
		out, err = exec.Command(
			filepath.Join(pkgDir, "bin", "kustomize"),
			"edit",
			"set",
			"image",
			fmt.Sprintf("%s=%s:%s", pdImagePlaceholder, stagingImage, stagingVersion)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to edit kustomize: %s, err: %v", out, err)
		}
		if platform == "windows" {
			out, err = exec.Command(
				filepath.Join(pkgDir, "bin", "kustomize"),
				"edit",
				"set",
				"image",
				fmt.Sprintf("%s-win=%s-win:%s", pdImagePlaceholder, stagingImage, stagingVersion)).CombinedOutput()
			if err != nil {
				return fmt.Errorf("failed to edit kustomize: %s, err: %v", out, err)
			}
		}
	}

	// setup service account file for secret creation
	tmpSaFile := filepath.Join(generateUniqueTmpDir(), "cloud-sa.json")
	defer removeDir(filepath.Dir(tmpSaFile))

	// Need to copy it to name the file "cloud-sa.json"
	out, err := exec.Command("cp", *saFile, tmpSaFile).CombinedOutput()
	if err != nil {
		return fmt.Errorf("error copying service account key: %s, err: %v", out, err)
	}
	defer shredFile(tmpSaFile)

	// deploy driver
	deployCmd := exec.Command(filepath.Join(pkgDir, "deploy", "kubernetes", "deploy-driver.sh"), "--skip-sa-check")
	deployCmd.Env = append(os.Environ(),
		fmt.Sprintf("GOPATH=%s", goPath),
		fmt.Sprintf("GCE_PD_SA_DIR=%s", filepath.Dir(tmpSaFile)),
		fmt.Sprintf("GCE_PD_DRIVER_VERSION=%s", deployOverlayName),
	)
	err = runCommand("Deploying driver", deployCmd)
	if err != nil {
		return fmt.Errorf("failed to deploy driver: %v", err)
	}
	klog.Infof("Deploying driver")
	// TODO (#139): wait for driver to be running
	if platform == "windows" {
		klog.Infof("Waiting 15 minutes for the driver to start on Windows")
		time.Sleep(15 * time.Minute)
	} else {
		klog.Infof("Waiting 5 minutes for the driver to start on Linux")
		time.Sleep(5 * time.Minute)
	}
	out, err = exec.Command("kubectl", "describe", "pods", "-n", driverNamespace).CombinedOutput()
	klog.Infof("describe pods \n %s", string(out))

	if err != nil {
		return fmt.Errorf("failed to describe pods: %v", err)
	}
	return nil
}

func deleteDriver(goPath, pkgDir, deployOverlayName string) error {
	deleteCmd := exec.Command(filepath.Join(pkgDir, "deploy", "kubernetes", "delete-driver.sh"))
	deleteCmd.Env = append(os.Environ(),
		fmt.Sprintf("GOPATH=%s", goPath),
		fmt.Sprintf("GCE_PD_DRIVER_VERSION=%s", deployOverlayName),
	)
	err := runCommand("Deleting driver", deleteCmd)
	if err != nil {
		return fmt.Errorf("failed to delete driver: %v", err)
	}
	return nil
}

func pushImage(pkgDir, registryName, rev, platform string) error {
	err := os.Setenv("REV", rev)
	if err != nil {
		return err
	}
	err = os.Setenv("REGISTRY_NAME", registryName)
	if err != nil {
		return err
	}
	var cmd *exec.Cmd

	cmd = exec.Command("make", "-C", pkgDir, "push-multiarch",
		fmt.Sprintf("REV=%s", rev),
		fmt.Sprintf("REGISTRY_NAME=%s", registryName))
	err = runCommand("Pushing GCP Container for Linux", cmd)
	if err != nil {
		return fmt.Errorf("failed to run make command for linux: err: %v", err)
	}
	return nil
}

func deleteImage(stagingImage, stagingVersion string) error {
	cmd := exec.Command("gcloud", "container", "images", "delete", fmt.Sprintf("%s:%s", stagingImage, stagingVersion), "--quiet")
	err := runCommand("Deleting GCR Container", cmd)
	if err != nil {
		return fmt.Errorf("failed to delete container image %s:%s: %s", stagingImage, stagingVersion, err)
	}
	return nil
}

// dumpDriverLogs will watch all pods in the driver namespace
// and copy its logs to the test artifacts directory, if set.
// It returns a context.CancelFunc that needs to be invoked when
// the test is finished.
func dumpDriverLogs() (context.CancelFunc, error) {
	// Dump all driver logs to the test artifacts
	artifactsDir, ok := os.LookupEnv("ARTIFACTS")
	if ok {
		client, err := getKubeClient()
		if err != nil {
			return nil, fmt.Errorf("failed to get kubeclient: %v", err)
		}
		out := podlogs.LogOutput{
			StatusWriter:  os.Stdout,
			LogPathPrefix: filepath.Join(artifactsDir, "pd-csi-driver") + "/",
		}
		ctx, cancel := context.WithCancel(context.Background())
		if err = podlogs.CopyAllLogs(ctx, client, driverNamespace, out); err != nil {
			return cancel, fmt.Errorf("failed to start pod logger: %v", err)
		}
		return cancel, nil
	}
	return nil, nil

}

// mergeArtifacts merges the results of doing multiple gingko runs, taking all junit files
// in the specified subdirectories of the artifacts directory and merging into a single
// file at the artifcats root.  If artifacts are not saved (ie, ARTIFACTS is not set),
// this is a no-op. See kubernetes-csi/csi-release-tools/prow.sh for the inspiration.
func mergeArtifacts(subdirectories []string) error {
	artifactsDir, ok := os.LookupEnv("ARTIFACTS")
	if !ok {
		// No artifacts, nothing to merge.
		return nil
	}
	var sourceDirs []string
	for _, subdir := range subdirectories {
		sourceDirs = append(sourceDirs, filepath.Join(artifactsDir, subdir))
	}
	return MergeJUnit("External Storage", sourceDirs, filepath.Join(artifactsDir, "junit_pdcsi.xml"))
}
