package app

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/argoproj/gitops-engine/pkg/sync/common"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/test/e2e/fixture"
)

type state = string

const (
	failed    = "failed"
	pending   = "pending"
	succeeded = "succeeded"
)

type Expectation func(c *Consequences) (state state, message string)

func OperationPhaseIs(expected common.OperationPhase) Expectation {
	return func(c *Consequences) (state, string) {
		operationState := c.app().Status.OperationState
		actual := common.OperationRunning
		if operationState != nil {
			actual = operationState.Phase
		}
		return simple(actual == expected, fmt.Sprintf("operation phase should be %s, is %s", expected, actual))
	}
}

func OperationMessageContains(text string) Expectation {
	return func(c *Consequences) (state, string) {
		operationState := c.app().Status.OperationState
		actual := ""
		if operationState != nil {
			actual = operationState.Message
		}
		return simple(strings.Contains(actual, text), fmt.Sprintf("operation message should contains '%s', got: '%s'", text, actual))
	}
}

func simple(success bool, message string) (state, string) {
	if success {
		return succeeded, message
	}
	return pending, message
}

func SyncStatusIs(expected v1alpha1.SyncStatusCode) Expectation {
	return func(c *Consequences) (state, string) {
		actual := c.app().Status.Sync.Status
		return simple(actual == expected, fmt.Sprintf("sync status to be %s, is %s", expected, actual))
	}
}

func HydrationPhaseIs(expected v1alpha1.HydrateOperationPhase) Expectation {
	return func(c *Consequences) (state, string) {
		actual := c.app().Status.SourceHydrator.CurrentOperation.Phase
		return simple(actual == expected, fmt.Sprintf("hydration phase to be %s, is %s", expected, actual))
	}
}

func Condition(conditionType v1alpha1.ApplicationConditionType, conditionMessage string) Expectation {
	return func(c *Consequences) (state, string) {
		got := c.app().Status.Conditions
		message := fmt.Sprintf("condition {%s %s} in %v", conditionType, conditionMessage, got)
		for _, condition := range got {
			if conditionType == condition.Type && strings.Contains(condition.Message, conditionMessage) {
				return succeeded, message
			}
		}
		return pending, message
	}
}

func NoConditions() Expectation {
	return func(c *Consequences) (state, string) {
		message := "no conditions"
		if len(c.app().Status.Conditions) == 0 {
			return succeeded, message
		}
		return pending, message
	}
}

func NoStatus() Expectation {
	return func(c *Consequences) (state, string) {
		message := "no status"
		if reflect.ValueOf(c.app().Status).IsZero() {
			return succeeded, message
		}
		return pending, message
	}
}

func StatusExists() Expectation {
	return func(c *Consequences) (state, string) {
		message := "status exists"
		if !reflect.ValueOf(c.app().Status).IsZero() {
			return succeeded, message
		}
		return pending, message
	}
}

func Namespace(name string, block func(app *v1alpha1.Application, ns *corev1.Namespace)) Expectation {
	return func(c *Consequences) (state, string) {
		ns, err := namespace(name)
		if err != nil {
			return failed, "namespace not found " + err.Error()
		}

		block(c.app(), ns)
		return succeeded, fmt.Sprintf("namespace %s assertions passed", name)
	}
}

func HealthIs(expected health.HealthStatusCode) Expectation {
	return func(c *Consequences) (state, string) {
		actual := c.app().Status.Health.Status
		return simple(actual == expected, fmt.Sprintf("health to should %s, is %s", expected, actual))
	}
}

func ResourceSyncStatusIs(kind, resource string, expected v1alpha1.SyncStatusCode) Expectation {
	return func(c *Consequences) (state, string) {
		actual := c.resource(kind, resource, "").Status
		return simple(actual == expected, fmt.Sprintf("resource '%s/%s' sync status should be %s, is %s", kind, resource, expected, actual))
	}
}

func ResourceSyncStatusWithNamespaceIs(kind, resource, namespace string, expected v1alpha1.SyncStatusCode) Expectation {
	return func(c *Consequences) (state, string) {
		actual := c.resource(kind, resource, namespace).Status
		return simple(actual == expected, fmt.Sprintf("resource '%s/%s' sync status should be %s, is %s", kind, resource, expected, actual))
	}
}

func ResourceHealthIs(kind, resource string, expected health.HealthStatusCode) Expectation {
	return func(c *Consequences) (state, string) {
		var actual health.HealthStatusCode
		resourceHealth := c.resource(kind, resource, "").Health
		if resourceHealth != nil {
			actual = resourceHealth.Status
		} else {
			// Some resources like ConfigMap may not have health status when they are okay
			actual = health.HealthStatusHealthy
		}
		return simple(actual == expected, fmt.Sprintf("resource '%s/%s' health should be %s, is %s", kind, resource, expected, actual))
	}
}

func ResourceHealthWithNamespaceIs(kind, resource, namespace string, expected health.HealthStatusCode) Expectation {
	return func(c *Consequences) (state, string) {
		var actual health.HealthStatusCode
		resourceHealth := c.resource(kind, resource, namespace).Health
		if resourceHealth != nil {
			actual = resourceHealth.Status
		} else {
			// Some resources like ConfigMap may not have health status when they are okay
			actual = health.HealthStatusHealthy
		}
		return simple(actual == expected, fmt.Sprintf("resource '%s/%s' health should be %s, is %s", kind, resource, expected, actual))
	}
}

func ResourceResultNumbering(num int) Expectation {
	return func(c *Consequences) (state, string) {
		actualNum := len(c.app().Status.OperationState.SyncResult.Resources)
		if actualNum < num {
			return pending, fmt.Sprintf("not enough results yet, want %d, got %d", num, actualNum)
		} else if actualNum == num {
			return succeeded, fmt.Sprintf("right number of results, want %d, got %d", num, actualNum)
		}
		return failed, fmt.Sprintf("too many results, want %d, got %d", num, actualNum)
	}
}

func ResourceResultIs(result v1alpha1.ResourceResult) Expectation {
	return func(c *Consequences) (state, string) {
		results := c.app().Status.OperationState.SyncResult.Resources
		for _, res := range results {
			if reflect.DeepEqual(*res, result) {
				return succeeded, fmt.Sprintf("found resource result %v", result)
			}
		}
		return pending, fmt.Sprintf("waiting for resource result %v in %v", result, results)
	}
}

func sameResourceResult(res1, res2 v1alpha1.ResourceResult) bool {
	return res1.Kind == res2.Kind &&
		res1.Group == res2.Group &&
		res1.Namespace == res2.Namespace &&
		res1.Name == res2.Name &&
		res1.SyncPhase == res2.SyncPhase &&
		res1.Status == res2.Status &&
		res1.HookPhase == res2.HookPhase
}

func ResourceResultMatches(result v1alpha1.ResourceResult) Expectation {
	return func(c *Consequences) (state, string) {
		results := c.app().Status.OperationState.SyncResult.Resources
		for _, res := range results {
			if sameResourceResult(*res, result) {
				re := regexp.MustCompile(result.Message)
				if re.MatchString(res.Message) {
					return succeeded, fmt.Sprintf("found resource result %v", result)
				}
			}
		}
		return pending, fmt.Sprintf("waiting for resource result %v in %v", result, results)
	}
}

func DoesNotExist() Expectation {
	return func(c *Consequences) (state, string) {
		_, err := c.get()
		if err != nil {
			if apierrors.IsNotFound(err) {
				return succeeded, "app does not exist"
			}
			return failed, err.Error()
		}
		return pending, "app should not exist"
	}
}

func DoesNotExistNow() Expectation {
	return func(c *Consequences) (state, string) {
		_, err := c.get()
		if err != nil {
			if apierrors.IsNotFound(err) {
				return succeeded, "app does not exist"
			}
			return failed, err.Error()
		}
		return failed, "app should not exist"
	}
}

func Pod(predicate func(p corev1.Pod) bool) Expectation {
	return func(_ *Consequences) (state, string) {
		pods, err := pods()
		if err != nil {
			return failed, err.Error()
		}
		for _, pod := range pods.Items {
			if predicate(pod) {
				return succeeded, fmt.Sprintf("pod predicate matched pod named '%s'", pod.GetName())
			}
		}
		return pending, "pod predicate does not match pods"
	}
}

func NotPod(predicate func(p corev1.Pod) bool) Expectation {
	return func(_ *Consequences) (state, string) {
		pods, err := pods()
		if err != nil {
			return failed, err.Error()
		}
		for _, pod := range pods.Items {
			if predicate(pod) {
				return pending, fmt.Sprintf("pod predicate matched pod named '%s'", pod.GetName())
			}
		}
		return succeeded, "pod predicate did not match any pod"
	}
}

func pods() (*corev1.PodList, error) {
	fixture.KubeClientset.CoreV1()
	pods, err := fixture.KubeClientset.CoreV1().Pods(fixture.DeploymentNamespace()).List(context.Background(), metav1.ListOptions{})
	return pods, err
}

func NoNamespace(name string) Expectation {
	return func(_ *Consequences) (state, string) {
		_, err := namespace(name)
		if err != nil {
			return succeeded, "namespace not found"
		}

		return failed, "found namespace " + name
	}
}

func namespace(name string) (*corev1.Namespace, error) {
	fixture.KubeClientset.CoreV1()
	return fixture.KubeClientset.CoreV1().Namespaces().Get(context.Background(), name, metav1.GetOptions{})
}

func event(namespace string, reason string, message string) Expectation {
	return func(c *Consequences) (state, string) {
		list, err := fixture.KubeClientset.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{
			FieldSelector: fields.SelectorFromSet(map[string]string{
				"involvedObject.name":      c.context.AppName(),
				"involvedObject.namespace": namespace,
			}).String(),
		})
		if err != nil {
			return failed, err.Error()
		}

		for i := range list.Items {
			event := list.Items[i]
			if event.Reason == reason && strings.Contains(event.Message, message) {
				return succeeded, fmt.Sprintf("found event with reason=%s; message=%s", reason, message)
			}
		}
		return failed, fmt.Sprintf("unable to find event with reason=%s; message=%s", reason, message)
	}
}

func Event(reason string, message string) Expectation {
	return event(fixture.TestNamespace(), reason, message)
}

func NamespacedEvent(namespace string, reason string, message string) Expectation {
	return event(namespace, reason, message)
}

// Success asserts that the last command was successful and that the output contains the given message.
func Success(message string, matchers ...func(string, string) bool) Expectation {
	if len(matchers) == 0 {
		matchers = append(matchers, strings.Contains)
	}
	match := func(actual, expected string) bool {
		for i := range matchers {
			if !matchers[i](actual, expected) {
				return false
			}
		}
		return true
	}
	return func(c *Consequences) (state, string) {
		if c.actions.lastError != nil {
			return failed, "error"
		}
		if !match(c.actions.lastOutput, message) {
			return failed, fmt.Sprintf("output did not contain '%s'", message)
		}
		return succeeded, fmt.Sprintf("no error and output contained '%s'", message)
	}
}

// Error asserts that the last command was an error with substring match
func Error(message, err string, matchers ...func(string, string) bool) Expectation {
	if len(matchers) == 0 {
		matchers = append(matchers, strings.Contains)
	}
	match := func(actual, expected string) bool {
		for i := range matchers {
			if !matchers[i](actual, expected) {
				return false
			}
		}
		return true
	}
	return func(c *Consequences) (state, string) {
		if c.actions.lastError == nil {
			return failed, "no error"
		}
		if !match(c.actions.lastOutput, message) {
			return failed, fmt.Sprintf("output does not contain '%s'", message)
		}
		if !match(c.actions.lastError.Error(), err) {
			return failed, fmt.Sprintf("error does not contain '%s'", err)
		}
		return succeeded, fmt.Sprintf("error '%s'", message)
	}
}

// ErrorRegex asserts that the last command was an error that matches given regex epxression
func ErrorRegex(messagePattern, err string) Expectation {
	return Error(messagePattern, err, func(actual, expected string) bool {
		return regexp.MustCompile(expected).MatchString(actual)
	})
}

// SuccessRegex asserts that the last command was successful and output matches given regex expression
func SuccessRegex(messagePattern string) Expectation {
	return Success(messagePattern, func(actual, expected string) bool {
		return regexp.MustCompile(expected).MatchString(actual)
	})
}
