package pipeline

import (
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// A Kotlin or Scala value initialised from a call binds through the return type
// that call's definition declares, as it does in the other typed languages.
func TestKotlinValueTypedByDeclaredReturn(t *testing.T) {
	got := langEdges(t, map[string]string{
		"app/Service.kt": "class Service {\n    fun compose(x: Int): Int { return x }\n}\n\n" +
			"fun makeService(): Service { return Service() }\n\n" +
			"fun untyped() = Service()\n\n" +
			"fun annotated() {\n    val svc = makeService()\n    svc.compose(2)\n}\n\n" +
			"fun unannotated() {\n    val svc = untyped()\n    svc.compose(3)\n}\n\n" +
			"fun rebound() {\n    var svc = makeService()\n    svc = Service()\n    svc.compose(4)\n}\n",
	})
	requireEdge(t, got, "app/Service.kt:annotated", "app/Service.kt:Service.compose", model.ConfidenceHigh)
	notTyped(t, got, "app/Service.kt:unannotated", "app/Service.kt:Service.compose")
	notTyped(t, got, "app/Service.kt:rebound", "app/Service.kt:Service.compose")
}

func TestScalaValueTypedByDeclaredReturn(t *testing.T) {
	got := langEdges(t, map[string]string{
		"app/Service.scala": "class Service {\n  def compose(x: Int): Int = x\n}\n\n" +
			"object Factory {\n  def makeService(): Service = new Service()\n" +
			"  def untyped() = new Service()\n" +
			"  def annotated(): Unit = {\n    val svc = makeService()\n    svc.compose(2)\n  }\n" +
			"  def unannotated(): Unit = {\n    val svc = untyped()\n    svc.compose(3)\n  }\n" +
			"  def rebound(): Unit = {\n    var svc = makeService()\n    svc = new Service()\n    svc.compose(4)\n  }\n}\n",
	})
	requireEdge(t, got, "app/Service.scala:Factory.annotated", "app/Service.scala:Service.compose", model.ConfidenceHigh)
	notTyped(t, got, "app/Service.scala:Factory.unannotated", "app/Service.scala:Service.compose")
	notTyped(t, got, "app/Service.scala:Factory.rebound", "app/Service.scala:Service.compose")
}
