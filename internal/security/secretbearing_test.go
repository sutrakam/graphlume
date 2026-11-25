package security

import (
	"strings"
	"testing"
)

// Every value below is synthetic: invented for this test, never a real
// credential.

// TestRedactsCredentialLiteralsBesideTheirName covers the shape the assignment
// scanner cannot see: a credential's name and its value passed as two literals
// on one line, most often a default handed to a getenv-style call. A default
// like this is exactly the literal that ends up equal to a deployed value.
func TestRedactsCredentialLiteralsBesideTheirName(t *testing.T) {
	cases := []struct {
		name, input, secret string
	}{
		{"python getenv default", `password = os.getenv("DB_PASSWORD", "synthPw_4821")`, "synthPw_4821"},
		{"method with a name, a reference and a sentinel", `self._reject("STORE_SECRET_KEY", self.STORE_SECRET_KEY, "fixture_pass77")`, "fixture_pass77"},
		{"go lookup with a fallback", `tok := envOr("SERVICE_TOKEN", "tk-90af13be55")`, "tk-90af13be55"},
		{"header set by name", `req.Header.Set("X-Api-Token", "c0ffee12abcd")`, "c0ffee12abcd"},
		{"javascript config call", "conf.get('apiKey', 'AbCd1234efGh')", "AbCd1234efGh"},
		{"symbols instead of digits", `os.environ.get("ADMIN_PASSWORD", "p@ss!word")`, "p@ss!word"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, findings := Redact(tc.input)
			if strings.Contains(out, tc.secret) {
				t.Fatalf("secret survived:\n%s", out)
			}
			if len(findings) == 0 {
				t.Fatalf("no finding for %q", tc.input)
			}
			again, more := Redact(out)
			if again != out || len(more) != 0 {
				t.Errorf("not idempotent: %q -> %q (%v)", out, again, more)
			}
		})
	}
}

// TestRedactsCommonCredentialShapes is the strengthened set: assignments in
// the syntaxes people write, connection strings, and authorization headers.
func TestRedactsCommonCredentialShapes(t *testing.T) {
	cases := []struct {
		name, input, secret string
	}{
		{"env assignment", "MINIO_SECRET_KEY=synth_pass123", "synth_pass123"},
		{"yaml", "  db_password: 'Zq9!synthetic'", "Zq9!synthetic"},
		{"kotlin val", `val apiKey = "k_8b1f2c9d7e"`, "k_8b1f2c9d7e"},
		{"go short assignment", `clientSecret := "s3cr3t-synth-55"`, "s3cr3t-synth-55"},
		{"postgres url", "DATABASE_URL=postgres://app:Synth9pw@db.internal:5432/orders", "Synth9pw"},
		{"mongodb srv url", `uri = "mongodb+srv://svc:Fake7Pass@cluster0.example.net/app"`, "Fake7Pass"},
		{"redis url with no user", "redis://:hunter77Synth@cache:6379/0", "hunter77Synth"},
		{"ado.net style string", `Server=db;User Id=app;Password=Synth4Pass;Encrypt=true`, "Synth4Pass"},
		{"jdbc query parameter", "jdbc:mysql://db/app?user=app&password=Synth4Jdbc", "Synth4Jdbc"},
		{"bearer header", `curl -H "Authorization: Bearer 9f8e7d6c5b4a39281706aa"`, "9f8e7d6c5b4a39281706aa"},
		{"basic header", "Authorization: Basic dXNlcjpzeW50aGV0aWMxMjM=", "dXNlcjpzeW50aGV0aWMxMjM="},
		{"shell default", `DB_PASS="${MONGO_PASSWORD:-synth4Mongo}"`, "synth4Mongo"},
		{"compose default", "      MINIO_SECRET_KEY: ${MINIO_SECRET_KEY-minio_synth_99}", "minio_synth_99"},
		{"assign default", "export API_TOKEN=${API_TOKEN:=tok_synth_123}", "tok_synth_123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, findings := Redact(tc.input)
			if strings.Contains(out, tc.secret) || len(findings) == 0 {
				t.Fatalf("secret survived:\n%s\nfindings: %v", out, findings)
			}
			if again, _ := Redact(out); again != out {
				t.Errorf("not idempotent: %q -> %q", out, again)
			}
		})
	}
}

// TestNamedLiteralsLeaveOrdinaryCodeAlone is the other half: a credential's
// name beside ordinary strings is code, not a leak.
func TestNamedLiteralsLeaveOrdinaryCodeAlone(t *testing.T) {
	for _, in := range []string{
		`form.add("password", "Password")`,
		`field("api_key", "API key", required=True)`,
		`log.info("token refreshed", "user_id")`,
		`os.getenv("DB_PASSWORD", "")`,
		`os.getenv("DB_PASSWORD", "${DB_PASSWORD}")`,
		`t("password", "change-me")`,
		`url_for("reset_password", "https://example.com/reset")`,
		`load("SECRET_FILE", "/etc/app/secret.txt")`,
		`Authorization: Basic authentication_required`,
		`password: ${DB_PASSWORD:?must be set in the environment}`,
		`host: ${DB_HOST:-localhost}`,
		`token: ${API_TOKEN:-}`,
		"type Credentials struct {\n\tPassword string `json:\"password\" db:\"password\"`\n}",
	} {
		if out, findings := Redact(in); len(findings) != 0 || out != in {
			t.Errorf("false positive on %q: %q %v", in, out, findings)
		}
	}
}

// TestSecretBearingPaths: the files a context pack and the UI never offer.
func TestSecretBearingPaths(t *testing.T) {
	yes := []string{
		".env", "deploy/.env", "infra/docker/.env.example", "infra/docker/.env.production.template",
		"svc/.env.demo", "app.env", "config/app.env.sample", ".env-staging",
		"certs/server.pem", "certs/server.key", "keys/store.p12", "keys/app.jks",
		"home/.ssh/id_rsa", "id_ed25519", "id_rsa.old", "config/credentials.json", "credentials",
		"credentials.yml", "secrets.yaml", "secrets/db.txt", "gcp/service-account-prod.json",
		"server.key.example",
	}
	no := []string{
		"src/env.go", "src/environment.ts", "docs/env.md", "certs/ca.crt", "home/.ssh/id_rsa.pub",
		"config/prod.yml", "internal/keys/keys.go", "src/credentials_test.go", "README.md", "pkg/envelope/x.go",
	}
	for _, p := range yes {
		if !IsSecretBearingPath(p) {
			t.Errorf("IsSecretBearingPath(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if IsSecretBearingPath(p) {
			t.Errorf("IsSecretBearingPath(%q) = true, want false", p)
		}
	}
}
