package pipeline

import (
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// A Kotlin parameter, a constructor property and a local with a stated type give
// their calls that type. Two classes declare `find`, so only the type can say
// which one a call reaches.
func TestKotlinStatedTypesDecideTheReceiver(t *testing.T) {
	got := langEdges(t, map[string]string{
		"app/Repo.kt":  "package app\n\nclass Repo {\n    fun find(id: Int): Int = id\n}\n",
		"app/Other.kt": "package app\n\nclass Other {\n    fun find(id: Int): Int = id\n}\n",
		"app/Service.kt": "package app\n\nclass Service(private val repo: Repo, val other: Other) {\n" +
			"    lateinit var late: Other\n\n" +
			"    fun viaParameter(r: Repo) {\n        r.find(1)\n    }\n\n" +
			"    fun viaProperty() {\n        repo.find(2)\n    }\n\n" +
			"    fun viaThis() {\n        this.other.find(3)\n    }\n\n" +
			"    fun viaLateinit() {\n        late.find(4)\n    }\n\n" +
			"    fun viaLocal() {\n        val x: Repo = make()\n        x.find(5)\n    }\n\n" +
			"    fun make(): Repo = Repo()\n}\n",
	})
	high := model.ConfidenceHigh
	requireEdge(t, got, "Service.viaParameter", "Repo.find", high)
	requireEdge(t, got, "Service.viaProperty", "Repo.find", high)
	requireEdge(t, got, "Service.viaThis", "Other.find", high)
	requireEdge(t, got, "Service.viaLateinit", "Other.find", high)
	requireEdge(t, got, "Service.viaLocal", "Repo.find", high)
	notTyped(t, got, "Service.viaParameter", "Other.find")
	notTyped(t, got, "Service.viaProperty", "Other.find")
	notTyped(t, got, "Service.viaThis", "Repo.find")
}
