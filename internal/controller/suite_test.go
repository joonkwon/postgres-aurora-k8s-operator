/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	postgresv1 "postgres-aurora-db-user/api/v1"
	"postgres-aurora-db-user/internal/services"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	dockerClient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/onsi/gomega/gexec"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var postgresContainerID string
var postgresPort string
var psDocker postgresDockerConfig = postgresDockerConfig{
	image:            "postgres:15.5",
	postgresPassword: "supersecretpassword",
}

type postgresDockerConfig struct {
	image            string
	postgresPassword string
}

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.28.3-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = postgresv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// k8s client for the controller (postgres DB/User)
	// this start of K8smanager is almost the same to main.go
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	// prepare postgres container to emulate RDS Postgres
	postgresContainerID, postgresPort, err = loadPostgresContainer(context.TODO(), psDocker)
	Expect(err).ToNot(HaveOccurred())

	// wait until the container spin up
	time.Sleep(5 * time.Second)

	localTokenProvider := &services.LocalPassProvider{
		Pass: psDocker.postgresPassword,
	}

	postgres := services.PostgresService{
		Host:           "127.0.0.1",
		MasterUsername: "postgres",
		Port:           postgresPort,
		Region:         "ca-central-1",
		SSLMode:        "disable",
		AuthProvider:   localTokenProvider,
	}

	// set up the defaul rds_iam role to emulate RDS
	err = setUpRDSIAMRole(postgres)
	Expect(err).ToNot(HaveOccurred(), "creating rds_iam role")

	err = (&DatabaseReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Postgres: &postgres,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&DBUserReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
		gexec.KillAndWait(4 * time.Second)

		// Teardown the test environment once controller is fnished.
		// Otherwise from Kubernetes 1.21+, teardon timeouts waiting on
		// kube-apiserver to return
		err := testEnv.Stop()
		Expect(err).ToNot(HaveOccurred())
	}()

})

var _ = AfterSuite(func() {
	By("tearing down Postgres Docker container")
	err := containerRemove(context.TODO(), postgresContainerID)
	Expect(err).NotTo(HaveOccurred())

	/*
			From Kubernetes 1.21+, when it tries to cleanup the test environment, there is
			a clash if a custom controller is created during testing. It would seem that
			the controller is still running and kube-apiserver will not respond to shutdown.
			This is the reason why teardown happens in BeforeSuite() after controller has stopped.
			The error shown is as documented in:
			https://github.com/kubernetes-sigs/controller-runtime/issues/1571
		/*
		/*
			By("tearing down the test environment")
			err := testEnv.Stop()
			Expect(err).NotTo(HaveOccurred())
	*/
})

func setUpRDSIAMRole(p services.PostgresService) (err error) {
	sql, err := p.GetMgmtDBClient()
	if err != nil {
		return
	}
	_, err = sql.Exec("CREATE ROLE rds_iam")
	if err != nil {
		return err
	}

	return
}

func loadPostgresContainer(ctx context.Context, config postgresDockerConfig) (containerID string, port string, err error) {
	cli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv, dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		return
	}
	defer cli.Close()

	reader, err := cli.ImagePull(ctx, config.image, types.ImagePullOptions{})
	if err != nil {
		return
	}

	defer reader.Close()
	// cli.ImagePull is asynchronous.
	// The reader needs to be read completely for the pull operation to complete.
	// If stdout is not required, consider using io.Discard instead of os.Stdout.
	io.Copy(io.Discard, reader)

	portNumber, err := getFreeHighPort()
	if err != nil {
		return
	}
	port = strconv.Itoa(portNumber)

	hostConfig := &container.HostConfig{
		PortBindings: nat.PortMap{
			"5432/tcp": []nat.PortBinding{
				{
					HostIP:   "0.0.0.0",
					HostPort: strconv.Itoa(portNumber),
				},
			},
		},
	}

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: config.image,
		Env: []string{
			fmt.Sprintf("POSTGRES_PASSWORD=%s", config.postgresPassword),
			"POSTGRES_USER=postgres",
		},
		Tty: false,
	}, hostConfig, nil, nil, "")
	if err != nil {
		return
	}
	containerID = resp.ID
	if err = cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return
	}
	return
}

func containerRemove(ctx context.Context, containerID string) error {
	cli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv, dockerClient.WithAPIVersionNegotiation())
	defer cli.Close()
	if err != nil {
		return err
	}
	fmt.Printf("Removing Container: %s\n", containerID)
	err = cli.ContainerRemove(ctx, containerID, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})
	if err != nil {
		fmt.Printf("Contianer Remove error: %s\n", err)
		return err
	}
	return nil
}

func getFreeHighPort() (port int, err error) {
	for i := 0; i < 5; i++ {
		port, err = getFreePort()
		if err == nil && port > 1024 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	return port, err
}

// GetFreePort asks the kernel for a free open port that is ready to use.
func getFreePort() (port int, err error) {
	var a *net.TCPAddr
	if a, err = net.ResolveTCPAddr("tcp", "localhost:0"); err == nil {
		var l *net.TCPListener
		if l, err = net.ListenTCP("tcp", a); err == nil {
			defer l.Close()
			return l.Addr().(*net.TCPAddr).Port, nil
		}
	}
	return
}
