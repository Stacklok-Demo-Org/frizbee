//
// Copyright 2024 Stacklok, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package replacer provide common replacer implementation
package replacer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"golang.org/x/sync/errgroup"

	"github.com/stacklok/frizbee/internal/store"
	"github.com/stacklok/frizbee/internal/traverse"
	"github.com/stacklok/frizbee/pkg/config"
	"github.com/stacklok/frizbee/pkg/ghrest"
	"github.com/stacklok/frizbee/pkg/interfaces"
	"github.com/stacklok/frizbee/pkg/replacer/actions"
	"github.com/stacklok/frizbee/pkg/replacer/image"
)

// Replacer replaces container image references in YAML files
type Replacer struct {
	regex  string
	parser interfaces.Parser
	interfaces.REST
	config.Config
}

// ReplaceResult holds a slice of all processed files along with a map of their modified content
type ReplaceResult struct {
	Processed []string
	Modified  map[string]string
}

// ListResult holds the result of the list methods
type ListResult struct {
	Processed []string
	Entities  []interfaces.EntityRef
}

// WithGitHubClient creates an authenticated GitHub client
func (r *Replacer) WithGitHubClient(token string) *Replacer {
	client := ghrest.NewClient(token)
	r.REST = client
	return r
}

// WithUserRegex sets a user-provided regex for the parser
func (r *Replacer) WithUserRegex(regex string) *Replacer {
	r.regex = regex
	return r
}

// New creates a new Replacer
func New(cfg *config.Config) *Replacer {
	// Return the replacer
	return &Replacer{
		Config: *cfg,
	}
}

// ParseGitHubActionString parses and returns the referenced entity pinned by its digest
func (r *Replacer) ParseGitHubActionString(ctx context.Context, entityRef string) (*interfaces.EntityRef, error) {
	r.parser = actions.New(r.regex)
	return r.parser.Replace(ctx, entityRef, r.REST, r.Config, nil)
}

// ParseGitHubActionsInPath parses and replaces all GitHub actions references in the provided directory
func (r *Replacer) ParseGitHubActionsInPath(ctx context.Context, dir string) (*ReplaceResult, error) {
	r.parser = actions.New(r.regex)
	return r.parsePathInFS(ctx, osfs.New(filepath.Dir(dir), osfs.WithBoundOS()), filepath.Base(dir))
}

// ParseGitHubActionsInFS parses and replaces all container image references in the provided file system
func (r *Replacer) ParseGitHubActionsInFS(ctx context.Context, bfs billy.Filesystem, base string) (*ReplaceResult, error) {
	r.parser = actions.New(r.regex)
	return r.parsePathInFS(ctx, bfs, base)
}

// ParseGitHubActionsInFile parses and replaces all GitHub actions references in the provided file
func (r *Replacer) ParseGitHubActionsInFile(ctx context.Context, f io.Reader, cache store.RefCacher) (bool, string, error) {
	r.parser = actions.New(r.regex)
	return r.parseAndReplaceReferencesInFile(ctx, f, cache)
}

// ListGitHibActionsInPath lists all GitHub actions references in the provided directory
func (r *Replacer) ListGitHibActionsInPath(dir string) (*ListResult, error) {
	r.parser = actions.New(r.regex)
	return r.listReferences(dir)
}

// ListGitHibActionsInFile lists all GitHub actions references in the provided file
func (r *Replacer) ListGitHibActionsInFile(f io.Reader) (*ListResult, error) {
	r.parser = actions.New(r.regex)
	found, err := r.listReferencesInFile(f)
	if err != nil {
		return nil, err
	}
	res := &ListResult{}
	res.Entities = found.ToSlice()

	// Sort the slice
	sort.Slice(res.Entities, func(i, j int) bool {
		return res.Entities[i].Name < res.Entities[j].Name
	})

	// All good
	return res, nil
}

// ParseContainerImageString parses and returns the referenced entity pinned by its digest
func (r *Replacer) ParseContainerImageString(ctx context.Context, entityRef string) (*interfaces.EntityRef, error) {
	r.parser = image.New(r.regex)
	return r.parser.Replace(ctx, entityRef, r.REST, r.Config, nil)
}

// ParseContainerImagesInPath parses and replaces all container image references in the provided directory
func (r *Replacer) ParseContainerImagesInPath(ctx context.Context, dir string) (*ReplaceResult, error) {
	r.parser = image.New(r.regex)
	return r.parsePathInFS(ctx, osfs.New(filepath.Dir(dir), osfs.WithBoundOS()), filepath.Base(dir))
}

// ParseContainerImagesInFS parses and replaces all container image references in the provided file system
func (r *Replacer) ParseContainerImagesInFS(ctx context.Context, bfs billy.Filesystem, base string) (*ReplaceResult, error) {
	r.parser = image.New(r.regex)
	return r.parsePathInFS(ctx, bfs, base)
}

// ParseContainerImagesInFile parses and replaces all container image references in the provided file
func (r *Replacer) ParseContainerImagesInFile(ctx context.Context, f io.Reader, cache store.RefCacher) (bool, string, error) {
	r.parser = image.New(r.regex)
	return r.parseAndReplaceReferencesInFile(ctx, f, cache)
}

// ListContainerImagesInPath lists all container image references in yaml, yml and dockerfiles present the provided directory
func (r *Replacer) ListContainerImagesInPath(dir string) (*ListResult, error) {
	r.parser = image.New(r.regex)
	return r.listReferences(dir)
}

// ListContainerImagesInFile lists all container image references in yaml, yml or dockerfile
func (r *Replacer) ListContainerImagesInFile(f io.Reader) (*ListResult, error) {
	r.parser = image.New(r.regex)
	found, err := r.listReferencesInFile(f)
	if err != nil {
		return nil, err
	}
	res := &ListResult{}
	res.Entities = found.ToSlice()

	// Sort the slice
	sort.Slice(res.Entities, func(i, j int) bool {
		return res.Entities[i].Name < res.Entities[j].Name
	})

	// All good
	return res, nil
}

func (r *Replacer) parsePathInFS(ctx context.Context, bfs billy.Filesystem, base string) (*ReplaceResult, error) {
	var eg errgroup.Group
	var mu sync.Mutex

	cache := store.NewRefCacher()

	res := ReplaceResult{
		Processed: make([]string, 0),
		Modified:  make(map[string]string),
	}

	// Traverse all YAML/YML files in dir
	err := traverse.YamlDockerfiles(bfs, base, func(path string) error {
		eg.Go(func() error {
			file, err := bfs.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", path, err)
			}
			// nolint:errcheck // ignore error
			defer file.Close()

			// Parse the content of the file and update the matching references
			modified, updatedFile, err := r.parseAndReplaceReferencesInFile(ctx, file, cache)
			if err != nil {
				return fmt.Errorf("failed to modify references in %s: %w", path, err)
			}

			mu.Lock()
			// Store the file name to the processed batch
			res.Processed = append(res.Processed, path)
			// Store the updated file content if it was modified
			if modified {
				res.Modified[path] = updatedFile
			}
			mu.Unlock()

			// All good
			return nil
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	// All good
	return &res, nil
}

func (r *Replacer) listReferences(dir string) (*ListResult, error) {
	var eg errgroup.Group
	var mu sync.Mutex

	basedir := filepath.Dir(dir)
	base := filepath.Base(dir)
	bfs := osfs.New(basedir, osfs.WithBoundOS())

	res := ListResult{
		Processed: make([]string, 0),
		Entities:  make([]interfaces.EntityRef, 0),
	}

	found := mapset.NewSet[interfaces.EntityRef]()

	// Traverse all related files
	err := traverse.YamlDockerfiles(bfs, base, func(path string) error {
		eg.Go(func() error {
			file, err := bfs.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", path, err)
			}
			// nolint:errcheck // ignore error
			defer file.Close()

			// Parse the content of the file and listReferences the matching references
			foundRefs, err := r.listReferencesInFile(file)
			if err != nil {
				return fmt.Errorf("failed to listReferences references in %s: %w", path, err)
			}

			// Store the file name to the processed batch
			mu.Lock()
			res.Processed = append(res.Processed, path)
			found = found.Union(foundRefs)
			mu.Unlock()

			// All good
			return nil
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	res.Entities = found.ToSlice()

	// Sort the slice
	sort.Slice(res.Entities, func(i, j int) bool {
		return res.Entities[i].Name < res.Entities[j].Name
	})

	// All good
	return &res, nil
}

func (r *Replacer) parseAndReplaceReferencesInFile(
	ctx context.Context,
	f io.Reader, cache store.RefCacher,
) (bool, string, error) {
	var contentBuilder strings.Builder
	var ret *interfaces.EntityRef

	modified := false

	// Compile the regular expression
	re, err := regexp.Compile(r.parser.GetRegex())
	if err != nil {
		return false, "", err
	}

	// Read the file line by line
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// See if we can match an entity reference in the line
		newLine := re.ReplaceAllStringFunc(line, func(matchedLine string) string {
			// Modify the reference in the line
			ret, err = r.parser.Replace(ctx, matchedLine, r.REST, r.Config, cache)
			if err != nil {
				// Return the original line as we don't want to update it in case something errored out
				return matchedLine
			}
			// Construct the new line, comments in dockerfiles are handled differently than yml files
			if strings.Contains(matchedLine, "FROM") {
				return fmt.Sprintf("%s%s:%s@%s", ret.Prefix, ret.Name, ret.Tag, ret.Ref)
			}
			return fmt.Sprintf("%s%s@%s # %s", ret.Prefix, ret.Name, ret.Ref, ret.Tag)
		})

		// Check if the line was modified and set the modified flag to true if it was
		if newLine != line {
			modified = true
		}

		// Write the line to the content builder buffer
		contentBuilder.WriteString(newLine + "\n")
	}

	// Check for errors during the scan
	if err := scanner.Err(); err != nil {
		return false, "", err
	}

	// Return the workflow content
	return modified, contentBuilder.String(), nil
}

// listReferencesInFile takes the given file reader and returns a map of all
// references, action or images, it found.
func (r *Replacer) listReferencesInFile(f io.Reader) (mapset.Set[interfaces.EntityRef], error) {
	found := mapset.NewSet[interfaces.EntityRef]()

	// Compile the regular expression
	re, err := regexp.Compile(r.parser.GetRegex())
	if err != nil {
		return nil, err
	}

	// Read the file line by line
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		// See if we can match an entity reference in the line
		foundEntries := re.FindAllString(line, -1)
		// nolint:gosimple
		if foundEntries != nil {
			for _, entry := range foundEntries {
				e, err := r.parser.ConvertToEntityRef(entry)
				if err != nil {
					continue
				}
				found.Add(*e)
			}
		}
	}

	// Check for errors during the scan
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Return the found references
	return found, nil
}
