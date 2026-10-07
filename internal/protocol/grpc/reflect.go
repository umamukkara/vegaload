package grpc

import (
	"context"
	"fmt"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// fetchFiles asks a server for the descriptors of a service and everything
// they import, using server reflection. It tries the v1 protocol first and
// the older v1alpha when the server does not have v1.
func fetchFiles(ctx context.Context, cc *grpc.ClientConn, service string) (*protoregistry.Files, error) {
	raw, err := reflectLoad(ctx, reflectV1{cc}, service)
	if status.Code(err) == codes.Unimplemented {
		raw, err = reflectLoad(ctx, reflectV1Alpha{cc}, service)
	}
	if err != nil {
		return nil, err
	}
	set := &descriptorpb.FileDescriptorSet{}
	for _, fd := range raw {
		set.File = append(set.File, fd)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("grpc: reading the server's descriptors: %w", err)
	}
	return files, nil
}

// reflector asks one reflection service for file descriptors, either of the
// file that holds a symbol or of a file by name. It returns the serialized
// FileDescriptorProtos the server answers with.
type reflector interface {
	fetch(ctx context.Context, symbol, filename string) ([][]byte, error)
}

// reflectLoad collects the file that holds service and all its imports.
func reflectLoad(ctx context.Context, r reflector, service string) (map[string]*descriptorpb.FileDescriptorProto, error) {
	files := map[string]*descriptorpb.FileDescriptorProto{}
	add := func(blobs [][]byte) error {
		for _, b := range blobs {
			fd := &descriptorpb.FileDescriptorProto{}
			if err := proto.Unmarshal(b, fd); err != nil {
				return fmt.Errorf("grpc: reading a descriptor from the server: %w", err)
			}
			files[fd.GetName()] = fd
		}
		return nil
	}
	blobs, err := r.fetch(ctx, service, "")
	if err != nil {
		return nil, err
	}
	if err := add(blobs); err != nil {
		return nil, err
	}
	// A server normally sends the imports too. Ask for any it left out,
	// and use the ones this program already knows (such as
	// google/protobuf/descriptor.proto) without asking.
	for {
		missing := ""
		for _, fd := range files {
			for _, dep := range fd.Dependency {
				if _, ok := files[dep]; !ok {
					missing = dep
					break
				}
			}
			if missing != "" {
				break
			}
		}
		if missing == "" {
			return files, nil
		}
		if known, err := protoregistry.GlobalFiles.FindFileByPath(missing); err == nil {
			files[missing] = protodesc.ToFileDescriptorProto(known)
			continue
		}
		blobs, err := r.fetch(ctx, "", missing)
		if err != nil {
			return nil, err
		}
		before := len(files)
		if err := add(blobs); err != nil {
			return nil, err
		}
		if len(files) == before {
			return nil, fmt.Errorf("grpc: the server did not send %s, which %s imports", missing, service)
		}
	}
}

type reflectV1 struct{ cc *grpc.ClientConn }

func (r reflectV1) fetch(ctx context.Context, symbol, filename string) ([][]byte, error) {
	stream, err := reflectionv1.NewServerReflectionClient(r.cc).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	req := &reflectionv1.ServerReflectionRequest{}
	if symbol != "" {
		req.MessageRequest = &reflectionv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol}
	} else {
		req.MessageRequest = &reflectionv1.ServerReflectionRequest_FileByFilename{FileByFilename: filename}
	}
	if err := stream.Send(req); err != nil {
		return nil, streamErr(err, func() error { _, e := stream.Recv(); return e })
	}
	_ = stream.CloseSend()
	resp, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if e := resp.GetErrorResponse(); e != nil {
		return nil, status.Error(codes.Code(e.ErrorCode), e.ErrorMessage)
	}
	return resp.GetFileDescriptorResponse().GetFileDescriptorProto(), nil
}

type reflectV1Alpha struct{ cc *grpc.ClientConn }

func (r reflectV1Alpha) fetch(ctx context.Context, symbol, filename string) ([][]byte, error) {
	stream, err := reflectionv1alpha.NewServerReflectionClient(r.cc).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	req := &reflectionv1alpha.ServerReflectionRequest{}
	if symbol != "" {
		req.MessageRequest = &reflectionv1alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol}
	} else {
		req.MessageRequest = &reflectionv1alpha.ServerReflectionRequest_FileByFilename{FileByFilename: filename}
	}
	if err := stream.Send(req); err != nil {
		return nil, streamErr(err, func() error { _, e := stream.Recv(); return e })
	}
	_ = stream.CloseSend()
	resp, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if e := resp.GetErrorResponse(); e != nil {
		return nil, status.Error(codes.Code(e.ErrorCode), e.ErrorMessage)
	}
	return resp.GetFileDescriptorResponse().GetFileDescriptorProto(), nil
}

// streamErr returns the real reason a Send failed. On a gRPC stream a failed
// Send only says io.EOF, and the status is in the next Recv.
func streamErr(sendErr error, recv func() error) error {
	if sendErr == io.EOF {
		if err := recv(); err != nil {
			return err
		}
	}
	return sendErr
}
