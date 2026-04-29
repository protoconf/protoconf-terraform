package importing

import (
	"path/filepath"
	"strings"

	"github.com/jhump/protoreflect/desc/builder"
	"github.com/mitchellh/cli"

	"github.com/protoconf/protoconf-terraform/pkg/importing/parse"
	"github.com/protoconf/protoconf-terraform/pkg/wktbuilders"
	"github.com/protoconf/protoconf/importers"
)

// Generator is used to create `terrafrom.proto` file with all of its
// available resources from the provider used in the current directory
// context.
type Generator struct {
	Providers  map[string]*ProviderImporter
	ImportPath string
	Importer   *importers.Importer
	ui         cli.Ui
}

// NewGenerator creates a new Generator
func NewGenerator(importPath, outputPath string, ui cli.Ui) *Generator {
	providersMap := make(map[string]*ProviderImporter)
	importer := importers.NewImporter("terraform/v1/terraform.proto", outputPath)
	file := importer.MasterFile
	file.SetProto3(true)
	file.SetPackageName("terraform.v1")

	main := builder.NewMessage("Terraform")
	resources := builder.NewMessage("Resources")
	data := builder.NewMessage("Datasources")
	providers := builder.NewMessage("Providers")
	main.AddNestedMessage(resources)
	main.AddField(builder.NewField("resource", builder.FieldTypeMessage(resources)).SetJsonName("resource"))
	main.AddNestedMessage(data)
	main.AddField(builder.NewField("data", builder.FieldTypeMessage(data)).SetJsonName("data"))
	main.AddNestedMessage(providers)
	main.AddField(builder.NewField("provider", builder.FieldTypeMessage(providers)).SetJsonName("provider"))
	err := file.TryAddMessage(main)
	if err != nil {
		panic(err)
	}
	return &Generator{
		ImportPath: importPath,
		Importer:   importer,
		Providers:  providersMap,
		ui:         ui,
	}
}

// PopulateProviders finds all providers available for the runtime context
// and populate the Generator with proto schemas from `providerimporter`
func (g *Generator) PopulateProviders() error {
	abs, err := filepath.Abs(g.ImportPath)
	if err != nil {
		return err
	}
	// dirs, err := filepath.Glob(abs)
	// if err != nil {
	// 	return err
	// }
	meta, err := parse.ParseTerraformSchema(abs)
	if err != nil {
		return err
	}

	for fqdn, schema := range meta.Schemas {
		parts := strings.Split(fqdn, "/")
		name := parts[len(parts)-1]
		p, err := NewProviderImporter(fqdn, schema, g.Importer, g.ui)
		if err != nil {
			return err
		}
		g.Providers[name] = p
	}

	return nil
}

func addVariableMessage(main *builder.MessageBuilder) error {
	local := builder.NewMessage("Variable")
	fieldType := builder.NewField("type", builder.FieldTypeString())
	fieldDesc := builder.NewField("description", builder.FieldTypeString())

	// default should be replaced eith google.protobuf.Value
	fieldDefault := builder.NewField("default", builder.FieldTypeString())
	local.AddField(fieldType)
	local.AddField(fieldDesc)
	local.AddField(fieldDefault)
	main.AddNestedMessage(local)
	main.AddField(builder.NewMapField("variable", builder.FieldTypeString(), builder.FieldTypeMessage(local)))
	return nil
}

func addOutputMessage(main *builder.MessageBuilder) error {
	local := builder.NewMessage("Output")
	fieldValue := builder.NewField("value", builder.FieldTypeString())
	local.AddField(fieldValue)
	main.AddNestedMessage(local)
	main.AddField(builder.NewMapField("output", builder.FieldTypeString(), builder.FieldTypeMessage(local)))
	return nil
}

func addLocalMessage(main *builder.MessageBuilder) error {
	main.AddField(builder.NewMapField("locals", builder.FieldTypeString(), builder.FieldTypeString()))
	return nil
}

func addModuleMessage(main *builder.MessageBuilder) error {
	valuebuilder := wktbuilders.StructBuilder.GetMessage("Struct")
	main.AddField(builder.NewMapField("module", builder.FieldTypeString(), builder.FieldTypeMessage(valuebuilder)))
	return nil
}

func addTerraformLocalBackend(b *builder.MessageBuilder, oof *builder.OneOfBuilder) error {
	l := builder.NewMessage("BackendLocal")
	l.AddField(builder.NewField("path", builder.FieldTypeString()))
	l.AddField(builder.NewField("workspace_dir", builder.FieldTypeString()).SetJsonName("workspace_dir"))
	b.AddNestedMessage(l)
	oof.AddChoice(builder.NewField("local", builder.FieldTypeMessage(l)))
	return nil
}

func defaultStringField(m *builder.MessageBuilder, name string, comments ...string) {
	c := builder.Comments{LeadingComment: strings.Join(comments, "\n")}
	m.AddField(builder.NewField(name, builder.FieldTypeString()).SetJsonName(name).SetComments(c))
}

func addTerraformRemoteBackend(b *builder.MessageBuilder, oof *builder.OneOfBuilder) error {
	l := builder.NewMessage("BackendRemote")
	defaultStringField(l, "hostname",
		"(Optional) The remote backend hostname to connect to. Defaults to app.terraform.io.",
	)
	defaultStringField(l, "organization", "(Required) The name of the organization containing the targeted workspace(s).")
	defaultStringField(l, "token", "(Optional) The token used to authenticate with the remote backend. We recommend omitting the token from the configuration, and instead using `terraform login` or manually configuring `credentials` in the CLI config file.")

	c := builder.Comments{LeadingComment: "(Required) A block specifying which remote workspace(s) to use. The workspaces block supports the following keys"}
	ws := builder.NewMessage("Workspace")
	defaultStringField(ws, "name", "(Optional) The full name of one remote workspace. When configured, only the default workspace can be used. This option conflicts with prefix.")
	defaultStringField(ws, "prefix", "(Optional) A prefix used in the names of one or more remote workspaces, all of which can be used with this configuration. The full workspace names are used in Terraform Cloud, and the short names (minus the prefix) are used on the command line for Terraform CLI workspaces. If omitted, only the default workspace can be used. This option conflicts with name.")
	l.AddNestedMessage(ws)
	l.AddField(builder.NewField("workspaces", builder.FieldTypeMessage(ws)).SetComments(c))

	b.AddNestedMessage(l)
	oof.AddChoice(builder.NewField("remote", builder.FieldTypeMessage(l)))
	return nil
}

func addTerraformS3Backend(b *builder.MessageBuilder, oof *builder.OneOfBuilder) error {
	l := builder.NewMessage("BackendS3")
	defaultStringField(l, "region")
	defaultStringField(l, "access_key")
	defaultStringField(l, "secret_key")
	defaultStringField(l, "iam_endpoint")                // (Optional) Custom endpoint for the AWS Identity and Access Management (IAM) API. This can also be sourced from the AWS_IAM_ENDPOINT environment variable.
	defaultStringField(l, "max_retries")                 // (Optional) The maximum number of times an AWS API request is retried on retryable failure. Defaults to 5.
	defaultStringField(l, "profile")                     // (Optional) Name of AWS profile in AWS shared credentials file (e.g. ~/.aws/credentials) or AWS shared configuration file (e.g. ~/.aws/config) to use for credentials and/or configuration. This can also be sourced from the AWS_PROFILE environment variable.
	defaultStringField(l, "shared_credentials_file")     // (Optional) Path to the AWS shared credentials file. Defaults to ~/.aws/credentials.
	defaultStringField(l, "skip_credentials_validation") // (Optional) Skip credentials validation via the STS API.
	defaultStringField(l, "skip_region_validation")      // (Optional) Skip validation of provided region name.
	defaultStringField(l, "skip_metadata_api_check")     // (Optional) Skip usage of EC2 Metadata API.
	defaultStringField(l, "sts_endpoint")                // (Optional) Custom endpoint for the AWS Security Token Service (STS) API. This can also be sourced from the AWS_STS_ENDPOINT environment variable.
	defaultStringField(l, "token")                       // (Optional) Multi-Factor Authentication (MFA) token. This can also be sourced from the AWS_SESSION_TOKEN environment variable.

	defaultStringField(l, "assume_role_duration_seconds")    // (Optional) Number of seconds to restrict the assume role session duration.
	defaultStringField(l, "assume_role_policy")              // (Optional) IAM Policy JSON describing further restricting permissions for the IAM Role being assumed.
	defaultStringField(l, "assume_role_policy_arns")         // (Optional) Set of Amazon Resource Names (ARNs) of IAM Policies describing further restricting permissions for the IAM Role being assumed.
	defaultStringField(l, "assume_role_tags")                // (Optional) Map of assume role session tags.
	defaultStringField(l, "assume_role_transitive_tag_keys") // (Optional) Set of assume role session tag keys to pass to any subsequent sessions.
	defaultStringField(l, "external_id")                     // (Optional) External identifier to use when assuming the role.
	defaultStringField(l, "role_arn")                        // (Optional) Amazon Resource Name (ARN) of the IAM Role to assume.
	defaultStringField(l, "session_name")                    // (Optional) Session name to use when assuming the role.

	defaultStringField(l, "bucket") // (Required) Name of the S3 Bucket.
	defaultStringField(l, "key")    // (Required) Path to the state file inside the S3 Bucket. When using a non-default workspace, the state path will be /workspace_key_prefix/workspace_name/key (see also the workspace_key_prefix configuration).

	defaultStringField(l, "acl")                  // (Optional) Canned ACL to be applied to the state file.
	defaultStringField(l, "encrypt")              // (Optional) Enable server side encryption of the state file.
	defaultStringField(l, "endpoint")             // (Optional) Custom endpoint for the AWS S3 API. This can also be sourced from the AWS_S3_ENDPOINT environment variable.
	defaultStringField(l, "force_path_style")     // (Optional) Enable path-style S3 URLs (https://<HOST>/<BUCKET> instead of https://<BUCKET>.<HOST>).
	defaultStringField(l, "kms_key_id")           // (Optional) Amazon Resource Name (ARN) of a Key Management Service (KMS) Key to use for encrypting the state.
	defaultStringField(l, "sse_customer_key")     // (Optional) The key to use for encrypting state with Server-Side Encryption with Customer-Provided Keys (SSE-C). This is the base64-encoded value of the key, which must decode to 256 bits. This can also be sourced from the AWS_SSE_CUSTOMER_KEY environment variable, which is recommended due to the sensitivity of the value. Setting it inside a terraform file will cause it to be persisted to disk in terraform.tfstate.
	defaultStringField(l, "workspace_key_prefix") // (Optional) Prefix applied to the state path inside the bucket. This is only relevant when using a non-default workspace. Defaults to env:.

	defaultStringField(l, "dynamodb_endpoint") // (Optional) Custom endpoint for the AWS DynamoDB API. This can also be sourced from the AWS_DYNAMODB_ENDPOINT environment variable.
	defaultStringField(l, "dynamodb_table")    // (Optional) Name of DynamoDB Table to use for state locking and consistency. The table must have a primary key named LockID with type of string. If not configured, state locking will be disabled.
	b.AddNestedMessage(l)
	oof.AddChoice(builder.NewField("s3", builder.FieldTypeMessage(l)))
	return nil
}

func addTerraformConsulBackend(b *builder.MessageBuilder, oof *builder.OneOfBuilder) error {
	l := builder.NewMessage("BackendConsul")
	defaultStringField(l, "path", "(Required) Path in the Consul KV store.")
	defaultStringField(l, "access_token", "(Optional) Consul Access Token.")
	defaultStringField(l, "address", "(Optional) DNS name and port of your Consul endpoint specified in the format dnsname:port.")
	defaultStringField(l, "scheme", "(Optional) Specifies what protocol to use when talking to the given address — either http or https. SSL support can also be triggered by setting the environment variable CONSUL_HTTP_SSL to true.")
	defaultStringField(l, "datacenter", "(Optional) The datacenter to use. Defaults to that of the agent.")
	defaultStringField(l, "http_auth", "(Optional) HTTP Basic Authentication credentials to be used when communicating with Consul, in the format of either user or user:pass.")
	defaultStringField(l, "gzip", "(Optional) true to compress the state data using gzip, or false (the default) to leave it uncompressed.")
	defaultStringField(l, "lock", "(Optional) false to disable locking. Defaults to true.")
	defaultStringField(l, "ca_file", "(Optional) A path to a PEM-encoded certificate authority used to verify the remote agent's certificate.")
	defaultStringField(l, "cert_file", "(Optional) A path to a PEM-encoded certificate provided to the remote agent; requires use of key_file.")
	defaultStringField(l, "key_file", "(Optional) A path to a PEM-encoded private key, required if cert_file is specified.")
	b.AddNestedMessage(l)
	oof.AddChoice(builder.NewField("consul", builder.FieldTypeMessage(l)))
	return nil
}

func addTerraformAzurermBackend(b *builder.MessageBuilder, oof *builder.OneOfBuilder) error {
	l := builder.NewMessage("BackendAzurerm")
	defaultStringField(l, "storage_account_name", "(Required) The Name of the Storage Account.")
	defaultStringField(l, "container_name", "(Required) The Name of the Storage Container within the Storage Account.")
	defaultStringField(l, "key", "(Required) The name of the Blob used to retrieve/store Terraform's State file inside the Storage Container.")
	defaultStringField(l, "environment", "(Optional) The Azure Environment which should be used. Possible values are public, china, german, stack and usgovernment. Defaults to public.")
	defaultStringField(l, "endpoint", "(Optional) The Custom Endpoint for Azure Resource Manager.")
	defaultStringField(l, "metadata_host", "(Optional) The Hostname of the Azure Metadata Service.")
	defaultStringField(l, "snapshot", "(Optional) Should the Blob used to store the Terraform Statefile be snapshotted before use? Defaults to false.")
	defaultStringField(l, "resource_group_name", "(Required for AzureAD/MSI auth) The Name of the Resource Group in which the Storage Account exists.")
	defaultStringField(l, "msi_endpoint", "(Optional) The path to a custom Managed Service Identity endpoint.")
	defaultStringField(l, "subscription_id", "(Optional) The Subscription ID in which the Storage Account exists.")
	defaultStringField(l, "tenant_id", "(Optional) The Tenant ID in which the Subscription exists.")
	defaultStringField(l, "client_id", "(Optional) The Client ID of the Service Principal.")
	defaultStringField(l, "client_secret", "(Optional) The Client Secret of the Service Principal.")
	defaultStringField(l, "client_certificate_password", "(Optional) The password associated with the Client Certificate specified in client_certificate_path.")
	defaultStringField(l, "client_certificate_path", "(Optional) The path to the PFX file used as the Client Certificate when authenticating as a Service Principal.")
	defaultStringField(l, "sas_token", "(Optional) The SAS Token used to access the Blob Storage Account.")
	defaultStringField(l, "access_key", "(Optional) The Access Key used to access the Blob Storage Account.")
	defaultStringField(l, "use_msi", "(Optional) Should Managed Service Identity authentication be used?")
	defaultStringField(l, "use_azuread_auth", "(Optional) Should Terraform use AzureAD authentication to access the Blob?")
	defaultStringField(l, "use_oidc", "(Optional) Should OIDC authentication be used?")
	defaultStringField(l, "oidc_request_token", "(Optional) The bearer token for the request to the OIDC provider.")
	defaultStringField(l, "oidc_request_url", "(Optional) The URL for the OIDC provider from which to request an ID token.")
	defaultStringField(l, "oidc_token", "(Optional) The OIDC ID token for use when authenticating using OIDC.")
	defaultStringField(l, "oidc_token_file_path", "(Optional) The path to a file containing an OIDC ID token for use when authenticating using OIDC.")
	b.AddNestedMessage(l)
	oof.AddChoice(builder.NewField("azurerm", builder.FieldTypeMessage(l)))
	return nil
}

func addTerraformGCSBackend(b *builder.MessageBuilder, oof *builder.OneOfBuilder) error {
	l := builder.NewMessage("BackendGCS")
	defaultStringField(l, "bucket", "(Required) The name of the GCS bucket. This bucket must exist before Terraform can be configured to use it.")
	defaultStringField(l, "credentials", "(Optional) Local path to Google Cloud Platform account credentials in JSON format. If unset, the path to your default credentials will be used (set via gcloud auth application-default login or GOOGLE_APPLICATION_CREDENTIALS).")
	defaultStringField(l, "impersonate_service_account", "(Optional) The service account to impersonate for accessing the State Bucket.")
	defaultStringField(l, "impersonate_service_account_delegates", "(Optional) The delegation chain for impersonating a service account.")
	defaultStringField(l, "access_token", "(Optional) A temporary OAuth 2.0 access token obtained from the Google Authorization server.")
	defaultStringField(l, "prefix", "(Optional) GCS prefix inside the bucket. Named states for workspaces are stored in an object called <prefix>/<name>.tfstate.")
	defaultStringField(l, "encryption_key", "(Optional) A 32 byte base64 encoded customer supplied encryption key (CSEK) used to encrypt all state.")
	defaultStringField(l, "kms_encryption_key", "(Optional) A Cloud KMS key (KMS_KEY) name used for encryption — projects/{project}/locations/{location}/keyRings/{keyring}/cryptoKeys/{name}.")
	defaultStringField(l, "storage_custom_endpoint", "(Optional) A URL containing the protocol, the DNS name pointing to a Private Service Connect endpoint, and the path for the Cloud Storage API (typically /storage/v1/b).")
	b.AddNestedMessage(l)
	oof.AddChoice(builder.NewField("gcs", builder.FieldTypeMessage(l)))
	return nil
}

func addTerraformHTTPBackend(b *builder.MessageBuilder, oof *builder.OneOfBuilder) error {
	l := builder.NewMessage("BackendHTTP")
	defaultStringField(l, "address", "(Required) The address of the REST endpoint.")
	defaultStringField(l, "update_method", "(Optional) HTTP method to use when updating state. Defaults to POST.")
	defaultStringField(l, "lock_address", "(Optional) The address of the lock REST endpoint. Defaults to disabled.")
	defaultStringField(l, "lock_method", "(Optional) The HTTP method to use when locking. Defaults to LOCK.")
	defaultStringField(l, "unlock_address", "(Optional) The address of the unlock REST endpoint. Defaults to disabled.")
	defaultStringField(l, "unlock_method", "(Optional) The HTTP method to use when unlocking. Defaults to UNLOCK.")
	defaultStringField(l, "username", "(Optional) The username for HTTP basic authentication.")
	defaultStringField(l, "password", "(Optional) The password for HTTP basic authentication.")
	defaultStringField(l, "skip_cert_verification", "(Optional) Whether to skip TLS verification. Defaults to false.")
	defaultStringField(l, "retry_max", "(Optional) The number of HTTP request retries. Defaults to 2.")
	defaultStringField(l, "retry_wait_min", "(Optional) The minimum time in seconds to wait between HTTP request attempts. Defaults to 1.")
	defaultStringField(l, "retry_wait_max", "(Optional) The maximum time in seconds to wait between HTTP request attempts. Defaults to 30.")
	defaultStringField(l, "client_ca_certificate_pem", "(Optional) A PEM-encoded CA certificate chain used by the client to verify server certificates during TLS authentication.")
	defaultStringField(l, "client_certificate_pem", "(Optional) A PEM-encoded certificate used by the server to verify the client during mutual TLS (mTLS) authentication.")
	defaultStringField(l, "client_private_key_pem", "(Optional) A PEM-encoded private key, required if client_certificate_pem is specified.")
	b.AddNestedMessage(l)
	oof.AddChoice(builder.NewField("http", builder.FieldTypeMessage(l)))
	return nil
}

func addTerraformKubernetesBackend(b *builder.MessageBuilder, oof *builder.OneOfBuilder) error {
	l := builder.NewMessage("BackendKubernetes")
	defaultStringField(l, "secret_suffix", "(Required) Suffix used when creating the secret. The secret will be named in the format: tfstate-{workspace}-{secret_suffix}.")
	defaultStringField(l, "labels", "(Optional) Map of additional labels to be applied to the secret and config map.")
	defaultStringField(l, "namespace", "(Optional) Namespace to store the secret in.")
	defaultStringField(l, "in_cluster_config", "(Optional) Used to authenticate to the cluster from inside a pod.")
	defaultStringField(l, "load_config_file", "(Optional) Load local kubeconfig.")
	defaultStringField(l, "host", "(Optional) The hostname (in form of URI) of the Kubernetes API.")
	defaultStringField(l, "username", "(Optional) The username to use for HTTP basic authentication when accessing the Kubernetes API.")
	defaultStringField(l, "password", "(Optional) The password to use for HTTP basic authentication when accessing the Kubernetes API.")
	defaultStringField(l, "insecure", "(Optional) Whether server should be accessed without verifying the TLS certificate.")
	defaultStringField(l, "client_certificate", "(Optional) PEM-encoded client certificate for TLS authentication.")
	defaultStringField(l, "client_key", "(Optional) PEM-encoded client certificate key for TLS authentication.")
	defaultStringField(l, "cluster_ca_certificate", "(Optional) PEM-encoded root certificates bundle for TLS authentication.")
	defaultStringField(l, "config_paths", "(Optional) A list of paths to kubeconfig files.")
	defaultStringField(l, "config_path", "(Optional) Path to the kubeconfig file. Can be sourced from KUBE_CONFIG_PATH.")
	defaultStringField(l, "config_context", "(Optional) Context to choose from the config file. Can be sourced from KUBE_CTX.")
	defaultStringField(l, "config_context_auth_info", "(Optional) Authentication info context of the kube config (--user flag in kubectl).")
	defaultStringField(l, "config_context_cluster", "(Optional) Cluster context of the kube config (--cluster flag in kubectl).")
	defaultStringField(l, "token", "(Optional) Token of your service account.")
	defaultStringField(l, "exec", "(Optional) Configuration block to use an exec-based credential plugin.")
	b.AddNestedMessage(l)
	oof.AddChoice(builder.NewField("kubernetes", builder.FieldTypeMessage(l)))
	return nil
}

func addTerraformPgBackend(b *builder.MessageBuilder, oof *builder.OneOfBuilder) error {
	l := builder.NewMessage("BackendPg")
	defaultStringField(l, "conn_str", "(Required) Postgres connection string; a postgres:// URL.")
	defaultStringField(l, "schema_name", "(Optional) Name of the automatically-managed Postgres schema to store state. Defaults to terraform_remote_state.")
	defaultStringField(l, "skip_schema_creation", "(Optional) If set to true, the Postgres schema must already exist. Defaults to false.")
	defaultStringField(l, "skip_table_creation", "(Optional) If set to true, the Postgres table must already exist.")
	defaultStringField(l, "skip_index_creation", "(Optional) If set to true, the Postgres index must already exist.")
	b.AddNestedMessage(l)
	oof.AddChoice(builder.NewField("pg", builder.FieldTypeMessage(l)))
	return nil
}

func addTerraformBackend(tf *builder.MessageBuilder) error {
	backend := builder.NewMessage("Backend")
	oneof := builder.NewOneOf("config")
	addTerraformLocalBackend(backend, oneof)
	addTerraformRemoteBackend(backend, oneof)
	addTerraformS3Backend(backend, oneof)
	addTerraformConsulBackend(backend, oneof)
	addTerraformAzurermBackend(backend, oneof)
	addTerraformGCSBackend(backend, oneof)
	addTerraformHTTPBackend(backend, oneof)
	addTerraformKubernetesBackend(backend, oneof)
	addTerraformPgBackend(backend, oneof)

	backend.AddOneOf(oneof)
	tf.AddNestedMessage(backend)
	tf.AddField(builder.NewField("backend", builder.FieldTypeMessage(backend)))
	return nil
}

func addTerraformConfigMessage(main *builder.MessageBuilder) error {
	local := builder.NewMessage("TerraformSettings")
	fieldTerraformVersion := builder.NewField("required_version", builder.FieldTypeString()).SetJsonName("required_version")
	msgProvider := builder.NewMessage("Provider")
	fieldSource := builder.NewField("source", builder.FieldTypeString())
	fieldVersion := builder.NewField("version", builder.FieldTypeString())
	msgProvider.AddField(fieldSource)
	msgProvider.AddField(fieldVersion)

	local.AddField(fieldTerraformVersion)
	local.AddNestedMessage(msgProvider)
	local.AddField(builder.NewMapField("required_providers", builder.FieldTypeString(), builder.FieldTypeMessage(msgProvider)).SetJsonName("required_providers"))
	addTerraformBackend(local)

	main.AddNestedMessage(local)
	main.AddField(builder.NewField("terraform", builder.FieldTypeMessage(local)))
	return nil
}

// Save will write all protofiles to disk
func (g *Generator) Save() error {
	main := g.Importer.MasterFile.GetMessage("Terraform")

	addVariableMessage(main)
	addOutputMessage(main)
	addLocalMessage(main)
	addModuleMessage(main)
	addTerraformConfigMessage(main)
	g.Importer.MasterFile.TryAddMessage(main)
	g.Importer.RegisterFile(metaFile)

	return g.Importer.SaveAll()

}
