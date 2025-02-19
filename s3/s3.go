package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/pidanou/c1-core/pkg/plugin"
	"github.com/pidanou/c1-core/pkg/plugin/proto"
)

type S3Connector struct {
	logger   hclog.Logger
	S3Client *s3.Client
}

type Options struct {
	Profile string   `json:"profile"`
	MaxKeys int32    `json:"max_keys"`
	Buckets []string `json:"buckets"`
	Region  string   `json:"region"`
}

func (o Options) String() string {
	buckets := strings.Join(o.Buckets, ",")
	return fmt.Sprint("profile: ", o.Profile, "maxkeys: ", o.MaxKeys, "buckets: ", buckets, "region: ", o.Region)
}

func (s *S3Connector) Sync(options string, cb plugin.CallbackHandler) error {

	var opts Options

	err := json.Unmarshal([]byte(options), &opts)
	if err != nil {
		s.logger.Error("Failed to unmarshal options", "error", err)
	}

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(opts.Region),
		config.WithSharedConfigProfile(opts.Profile),
	)

	// Create S3 service client
	svc := s3.NewFromConfig(cfg)
	s.S3Client = svc

	var buckets []string
	if opts.Buckets != nil {
		buckets = opts.Buckets
	} else {
		buckets, err = s.listBuckets()
		if err != nil {
			s.logger.Warn("Failed to list buckets", err)
			return err
		}
	}

	for _, bucket := range buckets {
		s.listObjects(bucket, opts, cb)
	}
	return nil
}

func (s *S3Connector) listBuckets() ([]string, error) {
	res := []string{}
	result, err := s.S3Client.ListBuckets(context.Background(), &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}
	for _, bucket := range result.Buckets {
		var noname = ""
		if bucket.Name == nil {
			bucket.Name = &noname
		}
		res = append(res, *bucket.Name)
	}
	return res, nil
}

func (s *S3Connector) listObjects(bucket string, opts Options, cb plugin.CallbackHandler) {
	params := &s3.ListObjectsV2Input{
		Bucket: &bucket,
	}
	p := s3.NewListObjectsV2Paginator(s.S3Client, params, func(o *s3.ListObjectsV2PaginatorOptions) {
		if v := int32(opts.MaxKeys); v != 0 {
			o.Limit = v
		}
	})
	var i int
	for p.HasMorePages() {
		i++
		page, err := p.NextPage(context.TODO())
		if err != nil {
			s.logger.Warn("failed to get page %v, %v", i, err)
		}

		res := []*proto.DataObject{}
		for _, obj := range page.Contents {
			arn := fmt.Sprintf(`arn:aws:s3:::%s/%s`, bucket, *obj.Key)
			lastModified := ""
			if obj.LastModified != nil {
				lastModified = obj.LastModified.Format("2006-01-02 15:04:05")
			}

			res = append(res, &proto.DataObject{
				RemoteId:     arn,
				ResourceName: *obj.Key,
				Uri:          arn,
				Metadata:     map[string]string{"last_modified": lastModified}})
		}
		// Ignore proto.Empty, error response
		_, _ = cb.Callback(&proto.SyncResponse{Response: res})
	}
}

var handshakeConfig = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "BASIC_PLUGIN",
	MagicCookieValue: "hello",
}

func main() {
	logger := hclog.New(&hclog.LoggerOptions{
		Level:      hclog.Trace,
		Output:     os.Stderr,
		JSONFormat: true,
	})

	connector := &S3Connector{
		logger: logger,
	}
	var pluginMap = map[string]goplugin.Plugin{
		"connector": &plugin.ConnectorGRPCPlugin{Impl: connector},
	}

	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}
