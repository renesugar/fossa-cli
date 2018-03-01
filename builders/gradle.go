package builders

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	logging "github.com/op/go-logging"

	"github.com/fossas/fossa-cli/config"
	"github.com/fossas/fossa-cli/module"
)

var gradleLogger = logging.MustGetLogger("gradle")

// GradleBuilder implements Builder for build.gradle builds
type GradleBuilder struct {
	GradleCmd     string
	GradleVersion string
}

// Initialize collects metadata on Java and Maven binaries
func (builder *GradleBuilder) Initialize() error {
	gradleLogger.Debug("Initializing Gradle builder...")

	// Set Gradle context variables
	gradleCmd, gradleVersionOut, err := which("--version --offline", os.Getenv("GRADLE_BINARY"), "./gradlew", "gradle")
	if err == nil {
		builder.GradleCmd = gradleCmd

		gradleVersionMatchRe := regexp.MustCompile(`Gradle ([0-9]+\.[0-9]+.\w+)`)
		match := gradleVersionMatchRe.FindStringSubmatch(gradleVersionOut)
		if len(match) == 2 {
			builder.GradleVersion = match[1]
		}
	}

	if builder.GradleCmd == "" {
		return fmt.Errorf("could not find Gradle binary or wrapper (try setting $GRADLE_BINARY)")
	}

	gradleLogger.Debugf("Done initializing Gradle builder: %#v", builder)
	return nil
}

// Build is not implemented
func (builder *GradleBuilder) Build(m module.Module, force bool) error {
	return nil
}

// Analyze parses the output of `gradle -q app:dependencies`
func (builder *GradleBuilder) Analyze(m module.Module, allowUnresolved bool) ([]module.Dependency, error) {
	gradleLogger.Debugf("Running Gradle analysis: %#v %#v in %s", m, allowUnresolved, m.Dir)

	// TODO: We need to let the user configure the right configurations
	dependenciesOutput, err := exec.Command(builder.GradleCmd, m.Name+":dependencies", "-q", "--configuration=compile", "--offline", "-a").Output()
	// Do not handle error, as gradle will exit with status 1 regardless
	if len(dependenciesOutput) == 0 || err != nil {
		return nil, fmt.Errorf("could not run Gradle task %s:dependencies", m.Name)
	}

	var deps []module.Dependency
	dependenciesRe := regexp.MustCompile(`- ([\w\.-]+):([\w\.-]+):([\w\.-]+)( -> ([\w\.-]+))?`)
	for _, line := range strings.Split(string(dependenciesOutput), "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 0 {
			parsedDependencyLine := dependenciesRe.FindStringSubmatch(trimmed)
			if len(parsedDependencyLine) >= 4 {
				groupID := parsedDependencyLine[1]
				artifactID := parsedDependencyLine[2]
				revisionID := parsedDependencyLine[3]
				if len(parsedDependencyLine) == 6 && parsedDependencyLine[5] != "" {
					revisionID = parsedDependencyLine[5]
				}
				gradleLogger.Debugf("Discovered maven artifact (%s, %s, %s)", trimmed, groupID, artifactID, revisionID)
				deps = append(deps, MavenArtifact{
					Name:    fmt.Sprintf("%s:%s", groupID, artifactID),
					Version: revisionID,
				})
			}
		}
	}

	gradleLogger.Debugf("Done running Gradle analysis: %#v", deps)
	return deps, nil
}

// IsBuilt is not implemented
func (builder *GradleBuilder) IsBuilt(m module.Module, allowUnresolved bool) (bool, error) {
	return true, nil
}

// IsModule is not implemented
func (builder *GradleBuilder) IsModule(target string) (bool, error) {
	return false, errors.New("IsModule is not implemented for GradleBuilder")
}

// DiscoverModules finds either a root build.gradle file in the specified dir
func (builder *GradleBuilder) DiscoverModules(dir string) ([]config.ModuleConfig, error) {

	if err := builder.Initialize(); err != nil {
		return nil, err
	}
	// Look for the root Gradle build
	// TODO: support custom *.gradle files
	if _, err := os.Stat(filepath.Join(dir, "build.gradle")); err == nil {
		// Use bare exec.Command as runLogged errors when resolving outside of dir
		taskListOutput, gradleTaskErr := exec.Command(builder.GradleCmd, "tasks", "--all", "-q", "-a", "--offline").Output()
		if len(taskListOutput) > 0 && gradleTaskErr == nil {
			// Search for subprojects using Gradle task list instead of grepping for build.gradle
			var moduleConfigurations []config.ModuleConfig
			// NOTE: this leaves out the root ("") dependencies task. To include, replace with `(\w+:)?dependencies -`
			taskListRe := regexp.MustCompile(`\w+:dependencies -`)
			for _, line := range strings.Split(string(taskListOutput), "\n") {
				trimmed := strings.TrimSpace(line)
				if len(trimmed) > 0 {
					depMatchIndicies := taskListRe.FindStringIndex(trimmed)
					if len(depMatchIndicies) > 0 && depMatchIndicies[0] == 0 {
						gradleTask := strings.Split(trimmed, " - ")[0]
						gradleLogger.Debugf("found gradle dependencies task: %s", gradleTask)
						moduleConfigurations = append(moduleConfigurations, config.ModuleConfig{
							Name: strings.Split(gradleTask, ":")[0], // Name is the gradle task name
							Path: "build.gradle",
							Type: "gradle",
						})
					}
				}
			}

			return moduleConfigurations, nil
		}

		// Fall back to "app" as default task, even though technically it would be "" (root)
		return []config.ModuleConfig{
			config.ModuleConfig{
				Name: "app",
				Path: "build.gradle",
				Type: "gradle",
			},
		}, nil
	}

	// TODO: support multiple build.gradle files
	return nil, nil
}
