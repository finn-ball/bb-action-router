package actionrouter

import (
	"path"
	"strings"
	"text/template"

	remoteexecution "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	pb "github.com/buildbarn/bb-action-router/pkg/proto/configuration/bb_docker_action_router"
	"github.com/buildbarn/bb-action-router/pkg/subtree"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type cacheDependencySubtreeOp struct {
	path   string
	helper []*template.Template
}

func validRelativePath(p string) bool {
	return p != "" && p != "." && !strings.HasPrefix(p, "/") && p != ".." && !strings.HasPrefix(p, "../") && path.Clean(p) == p && !strings.ContainsRune(p, 0)
}

func newCacheDependencySubtreeOp(c *pb.CacheDependencySubtree) (operation, error) {
	if !validRelativePath(c.Path) || len(c.HelperArguments) == 0 {
		return nil, status.Error(codes.InvalidArgument, "cache_dependency_subtree requires a normalized relative path and helper_arguments")
	}
	o := &cacheDependencySubtreeOp{path: c.Path}
	for _, arg := range c.HelperArguments {
		t, err := parseTemplate("dependency helper argument", arg)
		if err != nil {
			return nil, err
		}
		o.helper = append(o.helper, t)
	}
	return o, nil
}

func pathsOverlap(a, b string) bool {
	return a == b || a == "." || b == "." || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (o *cacheDependencySubtreeOp) apply(s *pipelineState) error {
	command, err := s.getCommand()
	if err != nil {
		return err
	}
	cwd := command.WorkingDirectory
	if cwd != "" && !validRelativePath(cwd) {
		return status.Error(codes.InvalidArgument, "unsupported working directory")
	}

	// Deriving the input root from the helper's cwd requires a real directory
	// chain. A symlink cwd could resolve to a different depth or subtree.
	current := s.action.InputRootDigest
	if cwd != "" {
		for _, part := range strings.Split(cwd, "/") {
			dir, err := loadDirectory(s.ctx, s.cas, s.maxMessageSize, current, s.digestFunction)
			if err != nil {
				return err
			}
			var next *remoteexecution.Digest
			for _, child := range dir.Directories {
				if child.Name == part {
					next = child.Digest
					break
				}
			}
			if next == nil {
				return status.Error(codes.InvalidArgument, "working directory is missing or traverses a symlink")
			}
			current = next
		}
	}
	if cwd == o.path || strings.HasPrefix(cwd, o.path+"/") {
		return status.Error(codes.InvalidArgument, "working directory is inside cached dependency")
	}
	outputs := append(append(append([]string{}, command.OutputPaths...), command.OutputFiles...), command.OutputDirectories...)
	for _, output := range outputs {
		if output != "" && !validRelativePath(output) {
			return status.Error(codes.InvalidArgument, "unsupported output path")
		}
		if pathsOverlap(path.Join(cwd, output), o.path) {
			return status.Error(codes.InvalidArgument, "output overlaps cached dependency")
		}
	}
	var args []string
	for _, t := range o.helper {
		arg, err := render(t, s)
		if err != nil {
			return err
		}
		if arg == "--" {
			return status.Error(codes.InvalidArgument, "helper_arguments must not contain --")
		}
		if arg != "" {
			args = append(args, arg)
		}
	}
	if len(args) == 0 {
		return status.Error(codes.InvalidArgument, "empty dependency helper command")
	}
	root, dep, err := o.extract(s, s.action.InputRootDigest, strings.Split(o.path, "/"))
	if err != nil {
		return err
	}
	d, err := s.digestFunction.NewDigestFromProto(dep)
	if err != nil {
		return err
	}
	// The command itself is the mount manifest: it is content-addressed and
	// includes the original subtree identity even though the input root omits it.
	levels := ""
	if cwd != "" {
		levels = strings.Repeat("../", len(strings.Split(cwd, "/")))
	}
	args = append(args, "--root-mode=tmpfs", "--dependency-tree="+subtree.Reference(d), "--dependency-path="+o.path, "--dependency-root="+levels+".", "--")
	command.Arguments = append(args, command.Arguments...)
	s.commandChanged = true
	s.action.InputRootDigest = root
	return nil
}

func (o *cacheDependencySubtreeOp) extract(s *pipelineState, root *remoteexecution.Digest, parts []string) (*remoteexecution.Digest, *remoteexecution.Digest, error) {
	directory, err := loadDirectory(s.ctx, s.cas, s.maxMessageSize, root, s.digestFunction)
	if err != nil {
		return nil, nil, err
	}
	for _, child := range directory.Directories {
		if child.Name != parts[0] {
			continue
		}
		var dep *remoteexecution.Digest
		if len(parts) == 1 {
			dep = child.Digest
			empty, err := putDirectory(s.ctx, s.cas, &remoteexecution.Directory{}, s.digestFunction)
			if err != nil {
				return nil, nil, err
			}
			child.Digest = empty.GetProto()
		} else {
			rewritten, selected, err := o.extract(s, child.Digest, parts[1:])
			if err != nil {
				return nil, nil, err
			}
			child.Digest = rewritten
			dep = selected
		}
		rewritten, err := putDirectory(s.ctx, s.cas, directory, s.digestFunction)
		if err != nil {
			return nil, nil, err
		}
		return rewritten.GetProto(), dep, nil
	}
	return nil, nil, status.Errorf(codes.InvalidArgument, "dependency path %q is missing or traverses a symlink", o.path)
}
