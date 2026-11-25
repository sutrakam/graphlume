package deploy

import "example.com/catalogue/schema"

// A nested definition is still a definition.
spec: {
	#Probe: {
		path:    string
		timeout: *5 | int
	}
	probes: [...#Probe]
	replicas: 3
}

// Several labels on one line declare the first at top level.
cluster: region: "eu-west-1"

#Rollout: {
	strategy: "rolling" | "recreate"
	#Step: {
		weight: int & >=0 & <=100
	}
}

_internal: {
	debug: false
}

service: schema.#Service & {
	name: "billing"
	port: 8443
}

#Config: [string]: string

tags: {
	"app.kubernetes.io/name": "billing"
}
