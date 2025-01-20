# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Dynamically calculate CAPI and CAPA versions from go cache, so that we use the right path when installing the CRDs during tests.
- Support reduced IAM permissions for instance profile of worker nodes

## [0.28.0] - 2024-09-20

### Changed

- Support new, plural `aws.giantswarm.io/irsa-trust-domains` annotation on the AWSCluster object that centrally defines which service account issuer domains to use. The previous annotation is supported for backward compatibility.

## [0.27.1] - 2024-08-21

### Fixed

- Disable logger development mode to avoid panicking, use zap as logger

## [0.27.0] - 2024-07-11

### Fixed

- IRSA bucket versioning on CAPA is "v3"

## [0.26.0] - 2024-07-09

### Changed

- Add `ec2:DescribeAvailabilityZones` to control plane template.

## [0.25.0] - 2024-06-06

### Changed

- Update CAPA CR version to `v1beta2`

## [0.24.1] - 2024-06-05

### Fixed

- Ignore not found errors when deleting IAM roles. This is to avoid blocking deletion of the CRs.

## [0.24.0] - 2024-04-29

### Changed

- Update all IRSA roles trusted policy.

## [0.23.0] - 2024-04-26

### Fixed

- Changed service account matching `StringLike` to accommodate wildcard full names.

## [0.22.0] - 2024-04-15

### Changed

- Add toleration for `node.cluster.x-k8s.io/uninitialized` taint.
- Remove toleration for old `node-role.kubernetes.io/master` taint.
- Allow ALB controller to be installed on any namespace.

## [0.21.1] - 2024-04-11

### Fixed

- Add retry logic for removing the finalizer to all reconcilers. This fixes the same bug as in 0.17.1 but for all reconcilers.

## [0.21.0] - 2024-03-20

### Changed

- Add finalizer to AWSCluster when reconciling AWSClusterTemplates. The AWSClusterTemplate won't block deletion of the AWSCluster, without which the operator cannot proceed with deletion

## [0.20.0] - 2024-03-20

### Changed

- Use a more relaxed trust identity policy for `Route53Manager` IAM role to allow running multiple external-dns instances in the same cluster.

## [0.19.0] - 2024-03-19

### Changed

- Use S3 bucket domain instead of CloundFront domain fo China regions.

## [0.18.0] - 2024-03-13

### Changed

- Change trust policy attach logic to recreate it for Route53 role.

## [0.17.1] - 2024-03-12

### Fixed

- Add retry logic for removing the finalizer. This fixes a bug where if another controller removes it's finalizer before reconciliation finishes, capa-iam-operator will not be able to remove its own.

## [0.17.0] - 2024-03-07

### Changed

- Create a IAM client with specific Region in order to work with AWS China partition.
- Adjust all IAM policies to include all AWS partitions.
- Change inline policy document attach logic to recreate it if it's already attached to the role.

## [0.16.0] - 2024-02-28

### Changed

- Use `cert-manager-app` as service account name for Cert Manager (changed in recent version of cert-manager-app).

### Fixed

- Use `/aws/` as `AWS_SHARED_CREDENTIALS_FILE` to overcome changes in base images.

## [0.15.0] - 2024-01-10

### Changed

- Configure `gsoci.azurecr.io` as the default container image registry.

### Fixed

- Remove unnecessary finalizers from configmap and AWSCluster.

## [0.14.0] - 2023-11-23

### Added

- Add IRSA role for `aws-efs-csi-driver` app.

## [0.13.2] - 2023-11-15

### Fixed

- Fix not deleting all IRSA roles.

## [0.13.1] - 2023-11-10

### Fixed

- Fix malformed cluster-autoscaler policy.

## [0.13.0] - 2023-11-10

### Added

- Add new IAM role for cluster-autoscaler.

## [0.12.0] - 2023-11-02

### Added

- Add tags from `AWSCluster.Spec.AdditionalTags` and `AWSManagedControlPlane.Spec.AdditionalTags` to  all created resources.
- Add IRSA role for EBS CSI driver.

## [0.11.0] - 2023-11-01

### Added

- Add `global.podSecurityStandards.enforced` value for PSS migration.

### Changed

- Remove SecretReconciler.
- Refactor Reconcilers.
- Do not panic when OIDC setting is missing for EKS cluster.

### Added

- Add new role for AWS Load Balancer Controller.
- Add tests for iam package.

## [0.10.0] - 2023-08-11

### Added

- Create `external-dns` and `cert-manager` IAM roles for IRSA for EKS clusters.

### Fixed

- Remove cloudfront secret dependency from reconcilers.

## [0.9.0] - 2023-06-08

### Changed

- Fetch IRSA secret resource just right befire creating IRSA role to avoid locking node role creation for control plane and workers.

### Added

- Add necessary values for PSS policy warnings.

## [0.8.0] - 2023-05-08

### Fixed

- Add `control-plane` finalizer to IRSA cloudfront secret.

## [0.7.0] - 2023-03-10

### Added

- Add finalizer to the IRSA cloudfront secret.
- Add deletion logic for the IRSA roles.
- Add IRSA support for `cert-manager-controller` service account

### Fixed

- Allow required volume types in PSP so that pods can still be admitted
- Make controllers consistently put the "allow both KIAM and IRSA" IAM policy
- Retry policy creation if referenced principal (the role ARN) is not available yet

## [0.6.0] - 2023-02-17

### Added

- Statements and actions to `route53` trust policy to support `cert-manager` with IRSA
- Added the use of the runtime/default seccomp profile.

## [0.5.1] - 2023-01-30

### Fixed

- Increased cpu & memory resources limits/requests.

## [0.5.0] - 2023-01-13

### Added

- Secrets reconciler for IRSA to support `external-dns`

## [0.4.5] - 2023-01-09

### Fixed

- Fix resources left behind on deletion
- Avoid distracting error logs for expected situations

## [0.4.4] - 2022-11-29

### Fixed

- Add `ec2:DescribeVolumesModifications` to the control-plane role so that resizing volumes work.

## [0.4.3] - 2022-11-24

### Fixed

- Check for other resources using the same IAM instance profile as the resource being deleted and skip deleting the IAM role if others found.

## [0.4.2] - 2022-11-01

### Changed

- `PodSecurityPolicy` are removed on newer k8s versions, so only apply it if object is registered in the k8s API.

### Added

- Tolerate running on control-plane nodes if workers unavailable

## [0.4.1] - 2022-07-13

## [0.4.0] - 2022-04-19

- Add VerticalPodAutoscaler CR.
- Add IAM role creation for bastion node.

## [0.3.2] - 2022-03-03

- Added to `aws-app-collection`.

## [0.3.0] - 2021-10-06

- Renamed from `capa-iam-controller` to `capa-iam-operator`

## [0.2.0] - 2021-07-23

- Restrict `secretmanager` service permissions to access secrets with CAPI prefix.
- Only watch for CRs with capi watch filter.
- AWSMachinteTemplate controller - only watch for CRs with control plane role.

## [0.1.1] - 2021-07-15

- Rename Route53 and KIAM role names to match previous naming scheme.

## [0.1.0] - 2021-07-14

- Implement `AWSMachineTemplate` reconciler.
- Implement `AWSMachinePool` reconciler.

[Unreleased]: https://github.com/giantswarm/capa-iam-operator/compare/v0.28.0...HEAD
[0.28.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.27.1...v0.28.0
[0.27.1]: https://github.com/giantswarm/capa-iam-operator/compare/v0.27.0...v0.27.1
[0.27.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.26.0...v0.27.0
[0.26.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.25.0...v0.26.0
[0.25.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.24.1...v0.25.0
[0.24.1]: https://github.com/giantswarm/capa-iam-operator/compare/v0.24.0...v0.24.1
[0.24.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.23.0...v0.24.0
[0.23.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.22.0...v0.23.0
[0.22.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.21.1...v0.22.0
[0.21.1]: https://github.com/giantswarm/capa-iam-operator/compare/v0.21.0...v0.21.1
[0.21.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.20.0...v0.21.0
[0.20.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.19.0...v0.20.0
[0.19.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.18.0...v0.19.0
[0.18.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.17.1...v0.18.0
[0.17.1]: https://github.com/giantswarm/capa-iam-operator/compare/v0.17.0...v0.17.1
[0.17.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.16.0...v0.17.0
[0.16.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.15.0...v0.16.0
[0.15.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.13.2...v0.14.0
[0.13.2]: https://github.com/giantswarm/capa-iam-operator/compare/v0.13.1...v0.13.2
[0.13.1]: https://github.com/giantswarm/capa-iam-operator/compare/v0.13.0...v0.13.1
[0.13.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/giantswarm/capa-iam-operator/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.4.5...v0.5.0
[0.4.5]: https://github.com/giantswarm/capa-iam-operator/compare/v0.4.4...v0.4.5
[0.4.4]: https://github.com/giantswarm/capa-iam-operator/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/giantswarm/capa-iam-operator/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/giantswarm/capa-iam-operator/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/giantswarm/capa-iam-operator/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/giantswarm/capa-iam-operator/compare/v0.3.0...v0.3.2
[0.3.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/giantswarm/capa-iam-operator/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/giantswarm/capa-iam-operator/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/giantswarm/capa-iam-operator/compare/v1.0.0...v0.1.0
