package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"
)

// TestValidate runs the checks the runtime runs before every Sync: the
// schemas, supportedFields and catalog this provider publishes, then Validate.
func TestValidate(t *testing.T) {
	t.Parallel()
	v := validationtest.New(t, New(), "../../charts/provider-documentdb/generated/provider-spec.yaml")

	tests := []struct {
		fixture string
		// old is the fixture of the stored Instance, for rules about updates.
		old string
		// want lists the field paths of the expected errors.
		want []string
	}{
		{fixture: "valid.yaml"},
		{fixture: "full.yaml"},
		{fixture: "long-name.yaml", want: []string{"metadata.name"}},
		{fixture: "four-replicas.yaml", want: []string{"spec.components[engine].replicas"}},
		{fixture: "node-port.yaml", want: []string{"spec.components[engine].service.serviceType"}},
		{fixture: "requests.yaml", want: []string{"spec.components[engine].resources.requests"}},
		{fixture: "scheduler-name.yaml", want: []string{"spec.components[engine].schedulingPolicy.schedulerName"}},
		{fixture: "gpu-limit.yaml", want: []string{"spec.components[engine].resources.limits"}},
		{fixture: "core-backup.yaml", want: []string{"spec.backup"}},
		{fixture: "bad-schedule.yaml", want: []string{"spec.parameters.backup.schedule"}},
		{fixture: "tls-both.yaml", want: []string{"spec.parameters.tls"}},
		{fixture: "smaller-disk.yaml", old: "valid.yaml", want: []string{"spec.components[engine].storage.size"}},
		{fixture: "bootstrap-added.yaml", old: "valid.yaml", want: []string{"spec.parameters.bootstrap"}},
		{fixture: "user-secret-added.yaml", old: "valid.yaml", want: []string{"spec.userSecretRef"}},
		{fixture: "full.yaml", old: "full.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.fixture+"<-"+tt.old, func(t *testing.T) {
			t.Parallel()
			in := validationtest.Load(t, "testdata/validate/"+tt.fixture)
			res := v.Run(in, nil)
			if tt.old != "" {
				res = v.Run(in, validationtest.Load(t, "testdata/validate/"+tt.old))
			}

			var got []string
			for _, err := range res.Errors {
				got = append(got, err.Field)
			}
			assert.Equal(t, tt.want, got, "errors: %v", res.Errors)
		})
	}
}
