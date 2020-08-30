# Kubernetes Development

## Manual

To build and install a development version of the driver:
```bash
$ make REGISTRY_NAME=gcr.io/PROJECT_ID push-multiarch

# Modify controller.yaml and node.yaml in ./deploy/kubernetes/dev to use dev image
$ GCE_PD_DRIVER_VERSION=dev
$ ./deploy/kubernetes/deploy-driver.sh
```

To bring down driver:
```bash
$ ./deploy/kubernetes/delete-driver.sh
```

## TODO Testing

