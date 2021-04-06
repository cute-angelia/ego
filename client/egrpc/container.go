package egrpc

import (
	"google.golang.org/grpc"

	"github.com/gotomicro/ego/core/econf"
	"github.com/gotomicro/ego/core/elog"
)

// Option 可选项
type Option func(c *Container)

// Container 容器
type Container struct {
	config *Config
	name   string
	logger *elog.Component
}

// DefaultContainer 默认容器
func DefaultContainer() *Container {
	return &Container{
		config: DefaultConfig(),
		logger: elog.EgoLogger.With(elog.FieldComponent(PackageName)),
	}
}

// Load 加载配置key
func Load(key string) *Container {
	c := DefaultContainer()
	if err := econf.UnmarshalKey(key, &c.config); err != nil {
		c.logger.Panic("parse config error", elog.FieldErr(err), elog.FieldKey(key))
		return c
	}
	c.logger = c.logger.With(elog.FieldComponentName(key))
	c.logger = c.logger.With(elog.FieldAddr(c.config.Addr))
	c.name = key
	return c
}

// Build 构建组件
func (c *Container) Build(options ...Option) *Component {
	if c.config.Debug {
		options = append(options, WithDialOption(grpc.WithChainUnaryInterceptor(debugUnaryClientInterceptor(c.logger, c.name, c.config.Addr))))
	}

	if c.config.EnableAppNameInterceptor {
		options = append(options, WithDialOption(grpc.WithChainUnaryInterceptor(appNameUnaryClientInterceptor())))
	}

	if c.config.EnableTimeoutInterceptor {
		options = append(options, WithDialOption(grpc.WithChainUnaryInterceptor(timeoutUnaryClientInterceptor(c.logger, c.config.ReadTimeout, c.config.SlowLogThreshold))))
	}

	if c.config.EnableTraceInterceptor {
		options = append(options,
			WithDialOption(grpc.WithChainUnaryInterceptor(traceUnaryClientInterceptor())),
		)
	}

	options = append(options,
		WithDialOption(grpc.WithChainUnaryInterceptor(loggerUnaryClientInterceptor(c.logger, c.config))),
	)

	if c.config.EnableMetricInterceptor {
		options = append(options,
			WithDialOption(grpc.WithChainUnaryInterceptor(metricUnaryClientInterceptor(c.name))),
		)
	}

	for _, option := range options {
		option(c)
	}

	return newComponent(c.name, c.config, c.logger)
}
