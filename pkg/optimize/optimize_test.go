package optimize

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testTerraformProto = `syntax = "proto3";
package terraform.v1;

import "terraform/aws/resources/v6/instance.proto";
import "terraform/random/resources/v3/pet.proto";
import "terraform/aws/datasources/v6/ami.proto";
import "terraform/random/provider/v3/random.proto";
import "terraform/aws/provider/v6/aws.proto";

message Terraform {
  Resources resource = 1;
  Datasources data = 2;
  Providers provider = 3;

  message Resources {
    map<string, terraform.aws.resources.v6.AwsInstance> aws_instance = 1;
    map<string, terraform.random.resources.v3.RandomPet> random_pet = 2;
  }
  message Datasources {
    map<string, terraform.aws.datasources.v6.AwsAmi> aws_ami = 1;
  }
  message Providers {
    repeated terraform.aws.provider.v6.Aws aws = 1;
    repeated terraform.random.provider.v3.Random random = 2;
  }
}
`

const testAwsInstanceProto = `syntax = "proto3";
package terraform.aws.resources.v6;
message AwsInstance { string ami = 1; }
`

const testRandomPetProto = `syntax = "proto3";
package terraform.random.resources.v3;
message RandomPet { string id = 1; }
`

const testAwsAmiProto = `syntax = "proto3";
package terraform.aws.datasources.v6;
message AwsAmi { string id = 1; }
`

const testAwsProviderProto = `syntax = "proto3";
package terraform.aws.provider.v6;
message Aws { string region = 1; }
`

const testRandomProviderProto = `syntax = "proto3";
package terraform.random.provider.v3;
message Random { string seed = 1; }
`

// Materialized output with only random_pet + random provider populated.
// AWS resources / datasources / provider are intentionally absent so the
// optimizer should drop them.
const testMaterialized = `{
  "protoFile": "terraform/v1/terraform.proto",
  "value": {
    "@type": "type.googleapis.com/terraform.v1.Terraform",
    "resource": { "random_pet": { "fido": {} } },
    "provider": { "random": [{}] }
  }
}`

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOptimize_PrunesUnusedFieldsAndImports(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mat := filepath.Join(root, "materialized_config")

	writeFile(t, src, "terraform/v1/terraform.proto", testTerraformProto)
	writeFile(t, src, "terraform/aws/resources/v6/instance.proto", testAwsInstanceProto)
	writeFile(t, src, "terraform/random/resources/v3/pet.proto", testRandomPetProto)
	writeFile(t, src, "terraform/aws/datasources/v6/ami.proto", testAwsAmiProto)
	writeFile(t, src, "terraform/aws/provider/v6/aws.proto", testAwsProviderProto)
	writeFile(t, src, "terraform/random/provider/v3/random.proto", testRandomProviderProto)
	writeFile(t, mat, "demo.materialized_JSON", testMaterialized)

	report, err := Optimize(Options{
		ProtoPath:       filepath.Join(src, "terraform/v1/terraform.proto"),
		MaterializedDir: mat,
		SrcRoot:         src,
	})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}

	wantUsedRes := []string{"random_pet"}
	if !reflect.DeepEqual(report.UsedResources, wantUsedRes) {
		t.Errorf("UsedResources = %v, want %v", report.UsedResources, wantUsedRes)
	}
	if len(report.UsedDatasources) != 0 {
		t.Errorf("UsedDatasources = %v, want []", report.UsedDatasources)
	}
	wantUsedProv := []string{"random"}
	if !reflect.DeepEqual(report.UsedProviders, wantUsedProv) {
		t.Errorf("UsedProviders = %v, want %v", report.UsedProviders, wantUsedProv)
	}
	wantRemovedRes := []string{"aws_instance"}
	if !reflect.DeepEqual(report.RemovedResources, wantRemovedRes) {
		t.Errorf("RemovedResources = %v, want %v", report.RemovedResources, wantRemovedRes)
	}
	wantRemovedDS := []string{"aws_ami"}
	if !reflect.DeepEqual(report.RemovedDatasources, wantRemovedDS) {
		t.Errorf("RemovedDatasources = %v, want %v", report.RemovedDatasources, wantRemovedDS)
	}
	wantRemovedProv := []string{"aws"}
	if !reflect.DeepEqual(report.RemovedProviders, wantRemovedProv) {
		t.Errorf("RemovedProviders = %v, want %v", report.RemovedProviders, wantRemovedProv)
	}

	// Verify the rewritten proto: must contain random_pet, must not contain
	// aws_instance / aws_ami / aws provider, and must not have orphaned
	// AwsInstanceEntry / AwsAmiEntry messages.
	out, err := os.ReadFile(filepath.Join(src, "terraform/v1/terraform.proto"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	mustContain := []string{
		"random_pet",
		"import \"terraform/random/resources/v3/pet.proto\";",
		"import \"terraform/random/provider/v3/random.proto\";",
	}
	mustNotContain := []string{
		"aws_instance",
		"aws_ami",
		"AwsInstanceEntry",
		"AwsAmiEntry",
		"terraform/aws/resources/v6/instance.proto",
		"terraform/aws/datasources/v6/ami.proto",
		"terraform/aws/provider/v6/aws.proto",
	}
	for _, want := range mustContain {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, s)
		}
	}
	for _, bad := range mustNotContain {
		if strings.Contains(s, bad) {
			t.Errorf("output unexpectedly contains %q", bad)
		}
	}
}

// Two separate materialized files, each with disjoint usage. Optimize
// must union them — keep aws_instance, random_pet, aws_ami; keep both
// providers; drop nothing.
const testMaterializedA = `{
  "protoFile": "terraform/v1/terraform.proto",
  "value": {
    "@type": "type.googleapis.com/terraform.v1.Terraform",
    "resource": { "random_pet": { "fido": {} } },
    "provider": { "random": [{}] }
  }
}`

const testMaterializedB = `{
  "protoFile": "terraform/v1/terraform.proto",
  "value": {
    "@type": "type.googleapis.com/terraform.v1.Terraform",
    "resource": { "aws_instance": { "web": {} } },
    "data":     { "aws_ami": { "ubuntu": {} } },
    "provider": { "aws": [{}] }
  }
}`

func TestOptimize_UnionsAcrossMultipleMaterializedFiles(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mat := filepath.Join(root, "materialized_config")

	writeFile(t, src, "terraform/v1/terraform.proto", testTerraformProto)
	writeFile(t, src, "terraform/aws/resources/v6/instance.proto", testAwsInstanceProto)
	writeFile(t, src, "terraform/random/resources/v3/pet.proto", testRandomPetProto)
	writeFile(t, src, "terraform/aws/datasources/v6/ami.proto", testAwsAmiProto)
	writeFile(t, src, "terraform/aws/provider/v6/aws.proto", testAwsProviderProto)
	writeFile(t, src, "terraform/random/provider/v3/random.proto", testRandomProviderProto)
	writeFile(t, mat, "alpha/a.materialized_JSON", testMaterializedA)
	writeFile(t, mat, "beta/b.materialized_JSON", testMaterializedB)

	report, err := Optimize(Options{
		ProtoPath:       filepath.Join(src, "terraform/v1/terraform.proto"),
		MaterializedDir: mat,
		SrcRoot:         src,
	})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}

	wantRes := []string{"aws_instance", "random_pet"}
	if !reflect.DeepEqual(report.UsedResources, wantRes) {
		t.Errorf("UsedResources = %v, want %v", report.UsedResources, wantRes)
	}
	wantDS := []string{"aws_ami"}
	if !reflect.DeepEqual(report.UsedDatasources, wantDS) {
		t.Errorf("UsedDatasources = %v, want %v", report.UsedDatasources, wantDS)
	}
	wantProv := []string{"aws", "random"}
	if !reflect.DeepEqual(report.UsedProviders, wantProv) {
		t.Errorf("UsedProviders = %v, want %v", report.UsedProviders, wantProv)
	}
	if len(report.RemovedResources)+len(report.RemovedDatasources)+len(report.RemovedProviders) != 0 {
		t.Errorf("expected no removals (every field is used across the two files); got R=%v D=%v P=%v",
			report.RemovedResources, report.RemovedDatasources, report.RemovedProviders)
	}
}

// Materialized output where the Terraform message is NOT the top-level
// @type — instead it sits as a field of an outer wrapper message. The
// scanner must recurse and find it.
const testMaterializedNested = `{
  "protoFile": "some/wrapper.proto",
  "value": {
    "@type": "type.googleapis.com/example.Wrapper",
    "name": "deploy",
    "staging": {
      "@type": "type.googleapis.com/terraform.v1.Terraform",
      "resource": { "aws_instance": { "web": {} } },
      "provider": { "aws": [{}] }
    },
    "production": {
      "@type": "type.googleapis.com/terraform.v1.Terraform",
      "resource": { "random_pet": { "name": {} } },
      "provider": { "random": [{}] }
    }
  }
}`

func TestOptimize_FindsNestedTerraformMessages(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mat := filepath.Join(root, "materialized_config")

	writeFile(t, src, "terraform/v1/terraform.proto", testTerraformProto)
	writeFile(t, src, "terraform/aws/resources/v6/instance.proto", testAwsInstanceProto)
	writeFile(t, src, "terraform/random/resources/v3/pet.proto", testRandomPetProto)
	writeFile(t, src, "terraform/aws/datasources/v6/ami.proto", testAwsAmiProto)
	writeFile(t, src, "terraform/aws/provider/v6/aws.proto", testAwsProviderProto)
	writeFile(t, src, "terraform/random/provider/v3/random.proto", testRandomProviderProto)
	writeFile(t, mat, "wrapper.materialized_JSON", testMaterializedNested)

	report, err := Optimize(Options{
		ProtoPath:       filepath.Join(src, "terraform/v1/terraform.proto"),
		MaterializedDir: mat,
		SrcRoot:         src,
	})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}

	wantRes := []string{"aws_instance", "random_pet"}
	if !reflect.DeepEqual(report.UsedResources, wantRes) {
		t.Errorf("UsedResources = %v, want %v (scanner must recurse into wrapper messages)", report.UsedResources, wantRes)
	}
	wantProv := []string{"aws", "random"}
	if !reflect.DeepEqual(report.UsedProviders, wantProv) {
		t.Errorf("UsedProviders = %v, want %v", report.UsedProviders, wantProv)
	}
	// aws_ami isn't used in either nested Terraform → must be removed.
	wantRemovedDS := []string{"aws_ami"}
	if !reflect.DeepEqual(report.RemovedDatasources, wantRemovedDS) {
		t.Errorf("RemovedDatasources = %v, want %v", report.RemovedDatasources, wantRemovedDS)
	}
}

func TestOptimize_RefusesEmptyUsage(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mat := filepath.Join(root, "materialized_config")
	if err := os.MkdirAll(mat, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, src, "terraform/v1/terraform.proto", testTerraformProto)
	writeFile(t, src, "terraform/aws/resources/v6/instance.proto", testAwsInstanceProto)
	writeFile(t, src, "terraform/random/resources/v3/pet.proto", testRandomPetProto)
	writeFile(t, src, "terraform/aws/datasources/v6/ami.proto", testAwsAmiProto)
	writeFile(t, src, "terraform/aws/provider/v6/aws.proto", testAwsProviderProto)
	writeFile(t, src, "terraform/random/provider/v3/random.proto", testRandomProviderProto)

	_, err := Optimize(Options{
		ProtoPath:       filepath.Join(src, "terraform/v1/terraform.proto"),
		MaterializedDir: mat,
		SrcRoot:         src,
	})
	if err == nil {
		t.Fatal("Optimize: expected error for empty materialized dir, got nil")
	}
	if !strings.Contains(err.Error(), "no terraform.v1.Terraform messages") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestOptimize_DeleteOrphansRespectsLoadStatements(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mat := filepath.Join(root, "materialized_config")

	writeFile(t, src, "terraform/v1/terraform.proto", testTerraformProto)
	writeFile(t, src, "terraform/aws/resources/v6/instance.proto", testAwsInstanceProto)
	writeFile(t, src, "terraform/random/resources/v3/pet.proto", testRandomPetProto)
	writeFile(t, src, "terraform/aws/datasources/v6/ami.proto", testAwsAmiProto)
	writeFile(t, src, "terraform/aws/provider/v6/aws.proto", testAwsProviderProto)
	writeFile(t, src, "terraform/random/provider/v3/random.proto", testRandomProviderProto)
	writeFile(t, mat, "demo.materialized_JSON", testMaterialized)

	// .mpconf that imports aws.proto via load(), even though no AWS
	// resources end up in the materialized output. The orphan sweep
	// must keep aws.proto so the .mpconf still parses.
	writeFile(t, src, "demo.mpconf", `load("//terraform/aws/provider/v6/aws.proto", "Aws")
def main():
    return {}
`)

	report, err := Optimize(Options{
		ProtoPath:       filepath.Join(src, "terraform/v1/terraform.proto"),
		MaterializedDir: mat,
		SrcRoot:         src,
		DeleteOrphans:   true,
	})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}

	for _, deleted := range report.WouldDeleteFiles {
		if strings.Contains(deleted, "aws/provider/v6/aws.proto") {
			t.Errorf("orphan sweep deleted %s, but it is load()'d by demo.mpconf", deleted)
		}
	}
	if _, err := os.Stat(filepath.Join(src, "terraform/aws/provider/v6/aws.proto")); err != nil {
		t.Errorf("aws.proto was deleted despite load() reference: %v", err)
	}
	// instance.proto and ami.proto are NOT loaded — they should be gone.
	if _, err := os.Stat(filepath.Join(src, "terraform/aws/resources/v6/instance.proto")); !os.IsNotExist(err) {
		t.Errorf("instance.proto should have been deleted; stat err=%v", err)
	}
}
