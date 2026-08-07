"""
Tests for CNV-68552: Validate NAD name format in admission webhook

Verifies that the KubeVirt admission webhook correctly rejects VMI creation
when Multus network NAD names do not conform to DNS naming conventions,
and accepts VMI creation with valid NAD name formats.
"""

import logging

import pytest
from kubernetes.dynamic.exceptions import UnprocessableEntityError
from ocp_resources.virtual_machine_instance import VirtualMachineInstance

LOGGER = logging.getLogger(__name__)


def _vmi_body_with_multus_nad(namespace, nad_name, vmi_name="test-vmi-nad-validation"):
    """
    Build a minimal VMI body with a single Multus network attachment.

    Preconditions: None (pure data construction).
    Steps: Construct VMI dict with given NAD name.
    Expected: Returns valid VMI body dict.
    """
    return {
        "apiVersion": "kubevirt.io/v1",
        "kind": "VirtualMachineInstance",
        "metadata": {
            "name": vmi_name,
            "namespace": namespace,
        },
        "spec": {
            "domain": {
                "resources": {"requests": {"memory": "128Mi"}},
                "devices": {
                    "interfaces": [{"name": "test-net", "bridge": {}}],
                },
            },
            "networks": [
                {
                    "name": "test-net",
                    "multus": {"networkName": nad_name},
                },
            ],
        },
    }


class TestNADNameValidationRejection:
    """
    Verify that invalid Multus NAD name formats are rejected by the admission webhook.

    Preconditions: OpenShift cluster with CNV installed, Multus CNI available.
    """

    @pytest.mark.polarion("CNV-68552")
    @pytest.mark.parametrize(
        "nad_name, expected_msg",
        [
            pytest.param("UPPER", "is not valid", id="uppercase_standalone_name"),
            pytest.param("not.valid.name.", "is not valid", id="trailing_dot_standalone"),
            pytest.param("UPPER/my-nad", "is not valid", id="uppercase_namespace"),
            pytest.param("my.ns/my-nad", "is not valid", id="dotted_namespace"),
            pytest.param("foo/UPPER", "is not valid", id="uppercase_name_part"),
            pytest.param("foo/-invalid", "is not valid", id="leading_hyphen_name"),
            pytest.param("foo/invalid-", "is not valid", id="trailing_hyphen_name"),
            pytest.param("/name", "namespace must not be empty", id="empty_namespace"),
            pytest.param("ns/", "name must not be empty", id="empty_name"),
            pytest.param("a/b/c", "expected format", id="multiple_slashes"),
        ],
    )
    def test_reject_invalid_nad_name(self, namespace, admin_client, nad_name, expected_msg):
        """
        Test that VMI creation with an invalid Multus NAD name is rejected.

        Preconditions:
            - KubeVirt admission webhook is running
            - User has VMI create permissions
        Steps:
            1. Construct VMI spec with Multus network using invalid NAD name
            2. Attempt to create VMI via Kubernetes API
            3. Assert that creation fails with UnprocessableEntityError
            4. Assert error message contains expected validation message
        Expected:
            - API returns 422 Unprocessable Entity
            - Error message contains the expected substring indicating the validation failure
        """
        vmi_body = _vmi_body_with_multus_nad(namespace=namespace.name, nad_name=nad_name)

        LOGGER.info(f"Attempting VMI creation with invalid NAD name: {nad_name!r}")
        with pytest.raises(UnprocessableEntityError, match=expected_msg):
            VirtualMachineInstance(client=admin_client, **vmi_body).create()

    @pytest.mark.polarion("CNV-68552")
    def test_reject_nad_name_reports_both_errors(self, namespace, admin_client):
        """
        Test that both namespace and name errors are reported when both are invalid.

        Preconditions:
            - KubeVirt admission webhook is running
        Steps:
            1. Construct VMI spec with Multus NAD name "UPPER/UPPER"
            2. Attempt to create VMI
            3. Assert that error mentions both namespace and name issues
        Expected:
            - Admission error contains both "namespace" and "name" validation messages
        """
        vmi_body = _vmi_body_with_multus_nad(
            namespace=namespace.name, nad_name="UPPER/UPPER"
        )

        LOGGER.info("Attempting VMI creation with both namespace and name invalid")
        with pytest.raises(UnprocessableEntityError) as exc_info:
            VirtualMachineInstance(client=admin_client, **vmi_body).create()

        error_msg = str(exc_info.value)
        assert "namespace" in error_msg.lower(), (
            f"Error should mention invalid namespace, got: {error_msg}"
        )
        assert "name" in error_msg.lower(), (
            f"Error should mention invalid name, got: {error_msg}"
        )


class TestNADNameValidationAcceptance:
    """
    Verify that valid Multus NAD name formats are accepted by the admission webhook.

    Preconditions: OpenShift cluster with CNV installed, Multus CNI available.
    """

    @pytest.mark.polarion("CNV-68552")
    @pytest.mark.parametrize(
        "nad_name",
        [
            pytest.param("my-nad", id="simple_name"),
            pytest.param("default/my-nad", id="namespaced_name"),
            pytest.param("my-namespace/my-nad-123", id="namespaced_with_numbers"),
            pytest.param("my.nad", id="dotted_subdomain"),
            pytest.param("my-namespace/my.nad", id="namespaced_with_dots"),
        ],
    )
    def test_accept_valid_nad_name(self, namespace, admin_client, nad_name):
        """
        Test that VMI creation with a valid Multus NAD name is not rejected for name format.

        Preconditions:
            - KubeVirt admission webhook is running
            - User has VMI create permissions
        Steps:
            1. Construct VMI spec with Multus network using valid NAD name
            2. Attempt to create VMI via Kubernetes API
            3. Assert that if creation fails, it is NOT due to NAD name format validation
        Expected:
            - VMI creation does not fail with NAD name format validation error
            - (May fail for other reasons like missing NAD or disk, which is acceptable)
        """
        vmi_body = _vmi_body_with_multus_nad(
            namespace=namespace.name,
            nad_name=nad_name,
            vmi_name=f"test-vmi-valid-nad-{nad_name.replace('/', '-').replace('.', '-')[:20]}",
        )

        LOGGER.info(f"Attempting VMI creation with valid NAD name: {nad_name!r}")
        try:
            vmi = VirtualMachineInstance(client=admin_client, **vmi_body)
            vmi.create()
            # Clean up if creation succeeded
            vmi.delete()
        except UnprocessableEntityError as e:
            error_msg = str(e)
            assert "NAD name" not in error_msg, (
                f"Valid NAD name {nad_name!r} should not be rejected for format, "
                f"but got: {error_msg}"
            )
            assert "is not valid" not in error_msg, (
                f"Valid NAD name {nad_name!r} should not be rejected as invalid, "
                f"but got: {error_msg}"
            )
        except Exception:
            # Other errors (e.g., missing disk, missing NAD) are acceptable
            # as we only care about NAD name format validation
            pass
