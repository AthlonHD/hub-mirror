package pkg

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
)

type Cli struct {
	cli        *client.Client
	repository string
	username   string
	auth       string
	log        io.Writer
}

func NewCli(ctx context.Context, repository, username, password string, log io.Writer) (*Cli, error) {
	if username == "" || password == "" {
		return nil, errors.New("username or password cannot be empty")
	}

	authConfig := registry.AuthConfig{
		Username:      username,
		Password:      password,
		ServerAddress: repository,
	}
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return nil, err
	}

	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}

	err = retryWithTimeout(ctx, registryLoginTimeout, retryAttempts, retryInitialDelay, func(attemptCtx context.Context) error {
		_, loginErr := cli.RegistryLogin(attemptCtx, authConfig)
		return loginErr
	})
	if err != nil {
		_ = cli.Close()
		return nil, err
	}

	return &Cli{
		cli:        cli,
		repository: repository,
		username:   username,
		auth:       base64.URLEncoding.EncodeToString(encodedJSON),
		log:        log,
	}, nil
}

type Output struct {
	Source string
	Target string
}

func (c *Cli) Source2Target(source string, platform string) (*Output, error) {
	if source == "" {
		return nil, errors.New("source is nil")
	}

	target := source

	if strings.Contains(source, "$") {
		parts := strings.Split(source, "$")
		source = parts[0]
		target = parts[1]
	}

	if !strings.Contains(target, ":") && strings.Contains(source, ":") {
		parts := strings.Split(source, ":")
		target += ":" + parts[1]
	}

	if platform != "" {
		if strings.Contains(target, ":") {
			parts := strings.Split(target, ":")
			target = parts[0] + "-" + strings.ReplaceAll(platform, "/", "-") + ":" + parts[1]
		} else {
			target += "-" + strings.ReplaceAll(platform, "/", "-")
		}
	}

	if c.repository == "" {
		target = "docker.io/" + c.username + "/" + strings.ReplaceAll(target, "/", ".")
	} else {
		target = c.repository + "/" + strings.ReplaceAll(target, "/", ".")
	}

	return &Output{Source: source, Target: target}, nil
}

func (c *Cli) PullTagPushImage(ctx context.Context, source, platform string) (*Output, error) {
	output, err := c.Source2Target(source, platform)
	if err != nil {
		return nil, err
	}

	if err := c.PullImage(ctx, output.Source, platform); err != nil {
		return nil, err
	}
	if err := c.cli.ImageTag(ctx, output.Source, output.Target); err != nil {
		return nil, err
	}
	if err := c.PushImage(ctx, output.Target, platform); err != nil {
		return nil, err
	}
	return output, nil
}

type errorMessage struct {
	Error string `json:"error"`
}

func (c *Cli) PullImage(ctx context.Context, image, platform string) error {
	return retryWithTimeout(ctx, imageTransferTimeout, retryAttempts, retryInitialDelay, func(attemptCtx context.Context) error {
		return c.pullImageOnce(attemptCtx, image, platform)
	})
}

func (c *Cli) pullImageOnce(ctx context.Context, image, platform string) error {
	pullOut, err := c.cli.ImagePull(ctx, image, types.ImagePullOptions{Platform: platform})
	if pullOut != nil {
		defer pullOut.Close()
	}
	if err != nil {
		return err
	}

	reader := bufio.NewReader(pullOut)
	for {
		streamBytes, readErr := reader.ReadBytes('\n')
		if len(streamBytes) > 0 {
			if c.log != nil {
				_, _ = c.log.Write(streamBytes)
			}
			var response errorMessage
			_ = json.Unmarshal(streamBytes, &response)
			if response.Error != "" {
				return errors.New(response.Error)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

func (c *Cli) PushImage(ctx context.Context, image, platform string) error {
	return retryWithTimeout(ctx, imageTransferTimeout, retryAttempts, retryInitialDelay, func(attemptCtx context.Context) error {
		return c.pushImageOnce(attemptCtx, image, platform)
	})
}

func (c *Cli) pushImageOnce(ctx context.Context, image, platform string) error {
	pushOut, err := c.cli.ImagePush(ctx, image, types.ImagePushOptions{
		RegistryAuth: c.auth,
		Platform:     platform,
	})
	if pushOut != nil {
		defer pushOut.Close()
	}
	if err != nil {
		return err
	}

	reader := bufio.NewReader(pushOut)
	for {
		streamBytes, readErr := reader.ReadBytes('\n')
		if len(streamBytes) > 0 {
			if c.log != nil {
				_, _ = c.log.Write(streamBytes)
			}
			var response errorMessage
			_ = json.Unmarshal(streamBytes, &response)
			if response.Error != "" {
				return errors.New(response.Error)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}
