# Contributing to Sandstore

Welcome to the Sandstore community!

We're thrilled that you're interested in contributing to this distributed systems learning project. Whether you're a student taking your first steps into distributed systems, an experienced engineer wanting to give back, or someone in between, your contributions are valuable and welcome.

## Ways to Contribute

There are many ways to contribute to Sandstore:

- **Code contributions**: Bug fixes, new features, performance improvements
- **Documentation**: Tutorials, API docs, architecture explanations
- **Testing**: Writing tests, reporting bugs, testing edge cases
- **Learning resources**: Examples, guides, educational content
- **Community**: Helping others in discussions, reviewing PRs

No contribution is too small - from fixing typos to implementing major features, everything helps make distributed systems more accessible to learners worldwide.

## Getting Started

### Development Environment Setup

1. **Follow the installation guide**: Complete the setup in [INSTALL.md](INSTALL.md)

2. **Fork the repository** on GitHub

3. **Clone your fork**:
   ```bash
   git clone https://github.com/YOUR_USERNAME/sandstore.git
   cd sandstore
   ```

4. **Add the upstream remote**:
   ```bash
   git remote add upstream https://github.com/AnishMulay/sandstore.git
   ```

5. **Verify your setup**:
   ```bash
   make test-server
   ```

### Development Workflow

1. **Create a feature branch**:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes** following our coding guidelines (see below)

3. **Test your changes**:
   ```bash
   make test
   make test-server
   ```

4. **Commit your changes** (see commit guidelines below)

5. **Push to your fork**:
   ```bash
   git push origin feature/your-feature-name
   ```

6. **Open a Pull Request** on GitHub

## Coding Guidelines

### Go Style Guide

We follow standard Go conventions with some project-specific guidelines:

**Naming Conventions:**
- Use `PascalCase` for exported functions, types, and constants
- Use `camelCase` for unexported functions and variables
- Interface names should end with `-er` (e.g., `FileService`, `ChunkReplicator`)
- Package names should be short, lowercase, and descriptive

**Code Organization:**
- Keep functions focused and small (ideally < 50 lines)
- Group related functionality in the same file
- Use meaningful variable names that explain intent
- Add comments for complex algorithms or distributed systems concepts

**Error Handling:**
- Always handle errors explicitly
- Use custom error types for domain-specific errors (see `internal/*/errors.go`)
- Provide context in error messages for debugging
- Log errors at appropriate levels

**Example:**
```go
// Good
func (fs *DefaultFileService) StoreFile(path string, data []byte) error {
    if len(data) == 0 {
        return NewInvalidFileError("file data cannot be empty")
    }
    
    chunks, err := fs.chunkData(data)
    if err != nil {
        return fmt.Errorf("failed to chunk file %s: %w", path, err)
    }
    
    // ... rest of implementation
}

// Avoid
func (fs *DefaultFileService) StoreFile(p string, d []byte) error {
    c, e := fs.chunkData(d)
    if e != nil {
        return e // Lost context about what failed
    }
    // ...
}
```

### Testing Standards

**Unit Tests:**
- Write tests for all public functions
- Use table-driven tests for multiple scenarios
- Mock external dependencies (network, disk I/O)
- Test error conditions, not just happy paths

**Integration Tests:**
- Test service interactions
- Verify distributed behavior (leader election, replication)
- Include failure scenarios

**Test File Organization:**
```
internal/
├── file_service/
│   ├── file_service.go
│   ├── file_service_test.go      # Unit tests
│   └── integration_test.go       # Integration tests
```

**Example Test:**
```go
func TestFileService_StoreFile(t *testing.T) {
    tests := []struct {
        name    string
        path    string
        data    []byte
        wantErr bool
    }{
        {
            name: "valid file",
            path: "test.txt", 
            data: []byte("hello world"),
            wantErr: false,
        },
        {
            name: "empty data",
            path: "empty.txt",
            data: []byte{},
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

### Documentation Standards

**Code Comments:**
- Document all exported functions and types
- Explain distributed systems concepts for learners
- Include examples for complex APIs

**README Updates:**
- Update feature lists when adding new capabilities
- Add examples for new functionality
- Keep the learning path current

## Branching Strategy

We use a simplified Git flow:

- **`main`**: Production-ready code, always stable
- **`feature/*`**: New features and enhancements
- **`bugfix/*`**: Bug fixes
- **`docs/*`**: Documentation improvements

### Branch Naming

- `feature/raft-log-compaction`
- `bugfix/chunk-replication-race-condition`
- `docs/architecture-deep-dive`

## Commit Message Guidelines

We follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:

```
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

**Types:**
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `test`: Adding or updating tests
- `refactor`: Code refactoring without behavior changes
- `perf`: Performance improvements
- `chore`: Maintenance tasks

**Examples:**
```
feat(raft): implement log compaction for metadata service

Add log compaction to prevent unbounded log growth in Raft consensus.
Includes snapshot creation and log truncation with configurable intervals.

Closes #123
```

```
fix(chunk): resolve race condition in chunk replication

The chunk replicator was not properly synchronizing access to the
replication queue, causing occasional data corruption.

Fixes #456
```

## Pull Request Process

### Before Submitting

1. **Sync with upstream**:
   ```bash
   git fetch upstream
   git rebase upstream/main
   ```

2. **Run all tests**:
   ```bash
   make test
   make test-server
   ```

3. **Check code formatting**:
   ```bash
   go fmt ./...
   go vet ./...
   ```

4. **Update documentation** if needed

### PR Description Template

```markdown
## Description
Brief description of changes and motivation.

## Type of Change
- [ ] Bug fix
- [ ] New feature  
- [ ] Documentation update
- [ ] Performance improvement
- [ ] Refactoring

## Testing
- [ ] Unit tests pass
- [ ] Integration tests pass
- [ ] Manual testing completed

## Learning Impact
How does this change help people learn distributed systems concepts?

## Checklist
- [ ] Code follows project style guidelines
- [ ] Self-review completed
- [ ] Documentation updated
- [ ] Tests added/updated
```

### Review Process

1. **Automated checks** must pass (tests, linting)
2. **Code review** by at least one maintainer
3. **Learning focus review**: Does this help people understand distributed systems?
4. **Final approval** and merge

We aim to review PRs within 48 hours. Don't hesitate to ping us if you haven't heard back!

## Reporting Issues

### Bug Reports

Use the bug report template and include:

- **Environment**: OS, Go version, Sandstore version
- **Steps to reproduce**: Minimal example that demonstrates the issue
- **Expected behavior**: What should happen
- **Actual behavior**: What actually happens
- **Logs**: Relevant log output from `logs/*/sandstore.log`

### Feature Requests

For new features, consider:

- **Learning value**: How does this help people understand distributed systems?
- **Use case**: What problem does this solve?
- **Implementation ideas**: Any thoughts on how to implement it?
- **Breaking changes**: Will this affect existing functionality?

## Learning-Focused Contributions

Since Sandstore is a learning platform, we especially value contributions that:

### Educational Content
- **Tutorials**: Step-by-step guides for distributed systems concepts
- **Examples**: Real-world scenarios and use cases
- **Explanations**: Comments and docs that explain the "why" behind the code

### Interactive Learning
- **Failure scenarios**: Scripts that simulate network partitions, node failures
- **Visualization tools**: Ways to see Raft consensus in action
- **Debugging guides**: How to troubleshoot common distributed systems issues

### Code Quality for Learning
- **Clear abstractions**: Interfaces that make concepts easy to understand
- **Modular design**: Components that can be studied independently
- **Comprehensive logging**: Visibility into distributed coordination

## Community Guidelines

### Be Welcoming
- Help newcomers get started
- Explain distributed systems concepts patiently
- Share learning resources and insights

### Be Constructive
- Provide specific, actionable feedback
- Suggest improvements rather than just pointing out problems
- Celebrate learning milestones and contributions

### Be Collaborative
- Ask questions when you don't understand
- Share knowledge and experience
- Work together to solve complex problems

## Recognition

We believe in recognizing contributions:

- **Contributors** are listed in our README
- **Significant contributions** get highlighted in release notes
- **Learning resources** are featured in our documentation
- **Community helpers** get special recognition

## Getting Help

Stuck on something? We're here to help:

- **GitHub Discussions**: For questions about distributed systems concepts
- **Issues**: For bugs and feature requests
- **Code Review**: For feedback on your contributions

Don't be shy - asking questions helps everyone learn!

## Current Contribution Priorities

Looking for ways to contribute? Here are our current focus areas:

### High Priority
- [ ] Raft log compaction implementation
- [ ] Enhanced failure detection and recovery
- [ ] Performance benchmarking suite
- [ ] Interactive learning tutorials

### Medium Priority  
- [ ] Web-based cluster monitoring dashboard
- [ ] Additional client examples and scenarios
- [ ] Improved error messages and debugging
- [ ] Cross-platform testing

### Learning Resources
- [ ] Raft consensus deep-dive tutorial
- [ ] Distributed systems failure scenarios guide
- [ ] Performance analysis and optimization guide
- [ ] Architecture decision records (ADRs)

---