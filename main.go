package main

import (
	"context"
	"flag"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/avast/retry-go"
	"github.com/hashicorp/go-hclog"
	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/protoconf/libprotoconf"
	"github.com/protoconf/protoconf-terraform/proto/protoconf_terraform/config/v1"
	protoconf "github.com/protoconf/protoconf/agent/api/proto/v1"
	"github.com/smintz/keygroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

var (
	logger = hclog.New(&hclog.LoggerOptions{
		Name:       "eggpack-terraform-plugin",
		Level:      hclog.Info,
		JSONFormat: true,
	})
	cliConfig = &config.TerraformPluginConfig{
		AgentAddress: ":4300",
		ConfigPath:   "test/main",
		LogLevel:     config.TerraformPluginConfig_LOG_LEVEL_INFO,
	}
)

func tfLogStr() string {
	if cliConfig.LogAsJson {
		return "TF_LOG=JSON"
	}
	return "TF_LOG=" + strings.TrimPrefix(cliConfig.LogLevel.String(), "LOG_LEVEL_")
}

func runTerraform(ctx context.Context, key string, tf *dynamic.Message) error {
	root, err := filepath.Abs(cliConfig.TerraformRoot)
	if err != nil {
		return err
	}
	workDir := filepath.Join(root, key)
	l := logger.Named("runner").With("key", key, "workdir", workDir)
	l.Info("creating workdir")
	if err = os.MkdirAll(workDir, 0755); err != nil {
		return err
	}
	l.Info("writing file")
	jsonBytes, err := tf.MarshalJSONIndent()
	if err != nil {
		return nil
	}
	if err = ioutil.WriteFile(filepath.Join(workDir, "main.tf.json"), jsonBytes, 0644); err != nil {
		return err
	}
	l.Info("running terraform init")
	cmd := exec.CommandContext(ctx, "terraform", "-chdir="+workDir, "init")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, tfLogStr())
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		l.Error("failed to run terraform init", "error", err)
		return err
	}
	l.Info("running terraform apply")
	cmd = exec.CommandContext(ctx, "terraform", "-chdir="+workDir, "apply", "-auto-approve")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, tfLogStr())
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		l.Error("failed to run terraform apply", "error", err)
		return err
	}
	return nil
}

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan bool, 1)
	pConfig := libprotoconf.NewConfig(cliConfig)
	flagset := flag.NewFlagSet("eggpack-terraform-plugin", flag.ExitOnError)
	pConfig.SetEnvKeyPrefix("PROTOCONF")
	if pConfig.Environment() != nil {
		logger.Error("failed to parse environment variables")
	}
	pConfig.PopulateFlagSet(flagset)
	logger.Info("starting")
	if flagset.Parse(os.Args[1:]) != nil {
		logger.Error("failed to parse flag data")
	}
	logger = hclog.New(&hclog.LoggerOptions{
		Name:       "eggpack-terraform-plugin",
		Level:      hclog.Level(cliConfig.LogLevel),
		JSONFormat: cliConfig.LogAsJson,
	})

	parser := &protoparse.Parser{ImportPaths: []string{"src", ""}}
	descriptors, err := parser.ParseFiles("terraform/v1/terraform.proto")
	if err != nil {
		logger.Error("failed to parse proto files", "error", err)
	}
	mf := dynamic.NewMessageFactoryWithDefaults()
	anyResolver := dynamic.AnyResolver(mf, descriptors...)

	conn, err := grpc.Dial(
		cliConfig.AgentAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		logger.Error("failed to connect to agent", "error", err)
		return
	}
	defer conn.Close()

	tfFunc := func(ctx context.Context, key string) {
		retry.Do(func() error {
			tfLogger := logger.With("key", key)
			tfLogger.Info("start watching")
			stub := protoconf.NewProtoconfServiceClient(conn)
			stream, err := stub.SubscribeForConfig(ctx, &protoconf.ConfigSubscriptionRequest{Path: key})
			if err != nil {
				tfLogger.Error("failed to create stream", "error", err)
				return err
			}

			for {
				tfLogger.Debug("waiting for update")
				update, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					if status.Code(err) == codes.Canceled {
						tfLogger.Info("stopping")
						break
					}
					tfLogger.Error("got error reading from stream", "error", err, "code", status.Code(err))
					return err
				}
				name, err := anyResolver.Resolve(update.GetValue().TypeUrl)
				if err != nil {
					tfLogger.Error("failed to resolve name", "error", err)
					return err
				}
				tfMsg, err := dynamic.AsDynamicMessage(name)
				if err != nil {
					tfLogger.Error("failed to create basic message", "error", err)
					return err
				}
				err = tfMsg.Unmarshal(update.GetValue().Value)
				if err != nil {
					tfLogger.Error("failed to unmarshal message from bytes", "error", err)
					return err
				}
				return runTerraform(ctx, key, tfMsg)

			}
			tfLogger.Info("stop watching")
			return nil
		})
	}

	kg := keygroup.NewKeyGroup(tfFunc)
	go retry.Do(func() error {
		stub := protoconf.NewProtoconfServiceClient(conn)
		ctx := context.Background()
		// ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		// defer cancel()
		stream, err := stub.SubscribeForConfig(ctx, &protoconf.ConfigSubscriptionRequest{Path: cliConfig.ConfigPath})
		if err != nil {
			logger.Error("failed to create stream", "error", err)
			return err
		}
		for {
			logger.Debug("waiting for list update")
			update, err := stream.Recv()
			if err == io.EOF {
				logger.Error("server stopped")
				return err
			}
			if status.Code(err) == codes.Canceled {
				logger.Error("stopping")
				break
			}
			if err != nil {
				logger.Error("Got error reading from stream", "code", status.Code(err), "error", err)
				return err
			}
			logger.Info("got update on main config", "key", cliConfig.ConfigPath, "result", update)
			var list = &config.SubscriptionConfig{}
			err = anypb.UnmarshalTo(update.GetValue(), list, proto.UnmarshalOptions{})
			if err != nil {
				logger.Error("failed to unmarsal message", "error", err)
			}
			logger.Info("result", "list", list)
			go kg.Update(list.Keys)
		}
		return nil
	})

	go func() {
		sig := <-sigs
		logger.Info("got signal", "signal", sig)
		done <- true
	}()

	<-done
	kg.CancelWait()
	logger.Info("exiting")

}
