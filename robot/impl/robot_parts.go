package robotimpl

import (
	"context"
	"fmt"

	"github.com/edaniels/golog"
	"github.com/go-errors/errors"
	"go.uber.org/multierr"

	"go.viam.com/utils"
	"go.viam.com/utils/pexec"

	"go.viam.com/core/arm"
	"go.viam.com/core/base"
	"go.viam.com/core/board"
	"go.viam.com/core/camera"
	"go.viam.com/core/config"
	"go.viam.com/core/gripper"
	"go.viam.com/core/grpc/client"
	"go.viam.com/core/lidar"
	"go.viam.com/core/robot"
	"go.viam.com/core/sensor"
	"go.viam.com/core/sensor/compass"
)

// robotParts are the actual parts that make up a robot.
type robotParts struct {
	remotes        map[string]*remoteRobot
	boards         map[string]*proxyBoard
	arms           map[string]*proxyArm
	grippers       map[string]*proxyGripper
	cameras        map[string]*proxyCamera
	lidars         map[string]*proxyLidar
	bases          map[string]*proxyBase
	sensors        map[string]sensor.Sensor
	functions      map[string]struct{}
	providers      map[string]*proxyProvider
	processManager pexec.ProcessManager
}

// newRobotParts returns a properly initialized set of parts.
func newRobotParts(logger golog.Logger) *robotParts {
	return &robotParts{
		remotes:        map[string]*remoteRobot{},
		boards:         map[string]*proxyBoard{},
		arms:           map[string]*proxyArm{},
		grippers:       map[string]*proxyGripper{},
		cameras:        map[string]*proxyCamera{},
		lidars:         map[string]*proxyLidar{},
		bases:          map[string]*proxyBase{},
		sensors:        map[string]sensor.Sensor{},
		functions:      map[string]struct{}{},
		providers:      map[string]*proxyProvider{},
		processManager: pexec.NewProcessManager(logger),
	}
}

// fixType ensures that the component has correct type information.
func fixType(c config.Component, whichType config.ComponentType, pos int) config.Component {
	if c.Type == "" {
		c.Type = whichType
	} else if c.Type != whichType {
		panic(fmt.Sprintf("different types (%s) != (%s)", whichType, c.Type))
	}
	return c
}

// addRemote adds a remote to the parts.
func (parts *robotParts) addRemote(r *remoteRobot, c config.Remote) {
	parts.remotes[c.Name] = r
}

// AddBoard adds a board to the parts.
func (parts *robotParts) AddBoard(b board.Board, c board.Config) {
	if proxy, ok := b.(*proxyBoard); ok {
		b = proxy.actual
	}
	parts.boards[c.Name] = newProxyBoard(b)
}

// AddArm adds an arm to the parts.
func (parts *robotParts) AddArm(a arm.Arm, c config.Component) {
	c = fixType(c, config.ComponentTypeArm, len(parts.arms))
	if proxy, ok := a.(*proxyArm); ok {
		a = proxy.actual
	}
	parts.arms[c.Name] = &proxyArm{actual: a}
}

// AddGripper adds a gripper to the parts.
func (parts *robotParts) AddGripper(g gripper.Gripper, c config.Component) {
	c = fixType(c, config.ComponentTypeGripper, len(parts.grippers))
	if proxy, ok := g.(*proxyGripper); ok {
		g = proxy.actual
	}
	parts.grippers[c.Name] = &proxyGripper{actual: g}
}

// AddCamera adds a camera to the parts.
func (parts *robotParts) AddCamera(c camera.Camera, cc config.Component) {
	cc = fixType(cc, config.ComponentTypeCamera, len(parts.cameras))
	if proxy, ok := c.(*proxyCamera); ok {
		c = proxy.actual
	}
	parts.cameras[cc.Name] = &proxyCamera{actual: c}
}

// AddLidar adds a lidar to the parts.
func (parts *robotParts) AddLidar(l lidar.Lidar, c config.Component) {
	c = fixType(c, config.ComponentTypeLidar, len(parts.lidars))
	if proxy, ok := l.(*proxyLidar); ok {
		l = proxy.actual
	}
	parts.lidars[c.Name] = &proxyLidar{actual: l}
}

// AddBase adds a base to the parts.
func (parts *robotParts) AddBase(b base.Base, c config.Component) {
	c = fixType(c, config.ComponentTypeBase, len(parts.bases))
	if proxy, ok := b.(*proxyBase); ok {
		b = proxy.actual
	}
	parts.bases[c.Name] = &proxyBase{actual: b}
}

// AddSensor adds a sensor to the parts.
func (parts *robotParts) AddSensor(s sensor.Sensor, c config.Component) {
	c = fixType(c, config.ComponentTypeSensor, len(parts.sensors))
	switch pType := s.(type) {
	case *proxySensor:
		parts.sensors[c.Name] = &proxySensor{actual: pType.actual}
	case *proxyCompass:
		parts.sensors[c.Name] = newProxyCompass(pType.actual)
	case *proxyRelativeCompass:
		parts.sensors[c.Name] = newProxyRelativeCompass(pType.actual)
	default:
		if r, ok := s.(compass.RelativeCompass); ok {
			parts.sensors[c.Name] = newProxyRelativeCompass(r)
		} else if cc, ok := s.(compass.Compass); ok {
			parts.sensors[c.Name] = newProxyCompass(cc)
		} else {
			parts.sensors[c.Name] = &proxySensor{actual: s}
		}
	}
}

// addFunction adds a function to the parts.
func (parts *robotParts) addFunction(name string) {
	parts.functions[name] = struct{}{}
}

// AddProvider adds a provider to the parts.
func (parts *robotParts) AddProvider(p robot.Provider, c config.Component) {
	if proxy, ok := p.(*proxyProvider); ok {
		p = proxy.actual
	}
	parts.providers[c.Name] = &proxyProvider{actual: p}
}

// RemoteNames returns the names of all remotes in the parts.
func (parts *robotParts) RemoteNames() []string {
	names := []string{}
	for k := range parts.remotes {
		names = append(names, k)
	}
	return names
}

// mergeNamesWithRemotes merges names from the parts itself as well as its
// remotes.
func (parts *robotParts) mergeNamesWithRemotes(names []string, namesFunc func(remote robot.Robot) []string) []string {
	// use this to filter out seen names and preserve order
	seen := utils.NewStringSet()
	for _, name := range names {
		seen[name] = struct{}{}
	}
	for _, r := range parts.remotes {
		remoteNames := namesFunc(r)
		for _, name := range remoteNames {
			if _, ok := seen[name]; ok {
				continue
			}
			names = append(names, name)
		}
	}
	return names
}

// ArmNames returns the names of all arms in the parts.
func (parts *robotParts) ArmNames() []string {
	names := []string{}
	for k := range parts.arms {
		names = append(names, k)
	}
	return parts.mergeNamesWithRemotes(names, robot.Robot.ArmNames)
}

// GripperNames returns the names of all grippers in the parts.
func (parts *robotParts) GripperNames() []string {
	names := []string{}
	for k := range parts.grippers {
		names = append(names, k)
	}
	return parts.mergeNamesWithRemotes(names, robot.Robot.GripperNames)
}

// CameraNames returns the names of all cameras in the parts.
func (parts *robotParts) CameraNames() []string {
	names := []string{}
	for k := range parts.cameras {
		names = append(names, k)
	}
	return parts.mergeNamesWithRemotes(names, robot.Robot.CameraNames)
}

// LidarNames returns the names of all lidars in the parts.
func (parts *robotParts) LidarNames() []string {
	names := []string{}
	for k := range parts.lidars {
		names = append(names, k)
	}
	return parts.mergeNamesWithRemotes(names, robot.Robot.LidarNames)
}

// BaseNames returns the names of all bases in the parts.
func (parts *robotParts) BaseNames() []string {
	names := []string{}
	for k := range parts.bases {
		names = append(names, k)
	}
	return parts.mergeNamesWithRemotes(names, robot.Robot.BaseNames)
}

// BoardNames returns the names of all boards in the parts.
func (parts *robotParts) BoardNames() []string {
	names := []string{}
	for k := range parts.boards {
		names = append(names, k)
	}
	return parts.mergeNamesWithRemotes(names, robot.Robot.BoardNames)
}

// SensorNames returns the names of all sensors in the parts.
func (parts *robotParts) SensorNames() []string {
	names := []string{}
	for k := range parts.sensors {
		names = append(names, k)
	}
	return parts.mergeNamesWithRemotes(names, robot.Robot.SensorNames)
}

// FunctionNames returns the names of all functions in the parts.
func (parts *robotParts) FunctionNames() []string {
	names := []string{}
	for k := range parts.functions {
		names = append(names, k)
	}
	return parts.mergeNamesWithRemotes(names, robot.Robot.FunctionNames)
}

// Clone provides a shallow copy of each part.
func (parts *robotParts) Clone() *robotParts {
	var clonedParts robotParts
	if len(parts.remotes) != 0 {
		clonedParts.remotes = make(map[string]*remoteRobot, len(parts.remotes))
		for k, v := range parts.remotes {
			clonedParts.remotes[k] = v
		}
	}
	if len(parts.boards) != 0 {
		clonedParts.boards = make(map[string]*proxyBoard, len(parts.boards))
		for k, v := range parts.boards {
			clonedParts.boards[k] = v
		}
	}
	if len(parts.arms) != 0 {
		clonedParts.arms = make(map[string]*proxyArm, len(parts.arms))
		for k, v := range parts.arms {
			clonedParts.arms[k] = v
		}
	}
	if len(parts.grippers) != 0 {
		clonedParts.grippers = make(map[string]*proxyGripper, len(parts.grippers))
		for k, v := range parts.grippers {
			clonedParts.grippers[k] = v
		}
	}
	if len(parts.cameras) != 0 {
		clonedParts.cameras = make(map[string]*proxyCamera, len(parts.cameras))
		for k, v := range parts.cameras {
			clonedParts.cameras[k] = v
		}
	}
	if len(parts.lidars) != 0 {
		clonedParts.lidars = make(map[string]*proxyLidar, len(parts.lidars))
		for k, v := range parts.lidars {
			clonedParts.lidars[k] = v
		}
	}
	if len(parts.bases) != 0 {
		clonedParts.bases = make(map[string]*proxyBase, len(parts.bases))
		for k, v := range parts.bases {
			clonedParts.bases[k] = v
		}
	}
	if len(parts.sensors) != 0 {
		clonedParts.sensors = make(map[string]sensor.Sensor, len(parts.sensors))
		for k, v := range parts.sensors {
			clonedParts.sensors[k] = v
		}
	}
	if len(parts.functions) != 0 {
		clonedParts.functions = make(map[string]struct{}, len(parts.functions))
		for k, v := range parts.functions {
			clonedParts.functions[k] = v
		}
	}
	if len(parts.providers) != 0 {
		clonedParts.providers = make(map[string]*proxyProvider, len(parts.providers))
		for k, v := range parts.providers {
			clonedParts.providers[k] = v
		}
	}
	if parts.processManager != nil {
		clonedParts.processManager = parts.processManager.Clone()
	}
	return &clonedParts
}

// Close attempts to close/stop all parts.
func (parts *robotParts) Close() error {
	var allErrs error
	if err := parts.processManager.Stop(); err != nil {
		allErrs = multierr.Combine(allErrs, errors.Errorf("error stopping process manager: %w", err))
	}

	for _, x := range parts.remotes {
		if err := utils.TryClose(x); err != nil {
			allErrs = multierr.Combine(allErrs, errors.Errorf("error closing remote: %w", err))
		}
	}

	for _, x := range parts.arms {
		if err := utils.TryClose(x); err != nil {
			allErrs = multierr.Combine(allErrs, errors.Errorf("error closing arm: %w", err))
		}
	}

	for _, x := range parts.grippers {
		if err := utils.TryClose(x); err != nil {
			allErrs = multierr.Combine(allErrs, errors.Errorf("error closing gripper: %w", err))
		}
	}

	for _, x := range parts.cameras {
		if err := utils.TryClose(x); err != nil {
			allErrs = multierr.Combine(allErrs, errors.Errorf("error closing camera: %w", err))
		}
	}

	for _, x := range parts.lidars {
		if err := utils.TryClose(x); err != nil {
			allErrs = multierr.Combine(allErrs, errors.Errorf("error closing lidar: %w", err))
		}
	}

	for _, x := range parts.bases {
		if err := utils.TryClose(x); err != nil {
			allErrs = multierr.Combine(allErrs, errors.Errorf("error closing base: %w", err))
		}
	}

	for _, x := range parts.boards {
		if err := utils.TryClose(x); err != nil {
			allErrs = multierr.Combine(allErrs, errors.Errorf("error closing board: %w", err))
		}
	}

	for _, x := range parts.sensors {
		if err := utils.TryClose(x); err != nil {
			allErrs = multierr.Combine(allErrs, errors.Errorf("error closing sensor: %w", err))
		}
	}

	return allErrs
}

// processConfig ingests a given config and constructs all constituent parts.
func (parts *robotParts) processConfig(
	ctx context.Context,
	config *config.Config,
	robot *mutableRobot,
	logger golog.Logger,
) error {
	if err := parts.newProcesses(ctx, config.Processes); err != nil {
		return err
	}

	if err := parts.newRemotes(ctx, config.Remotes, logger); err != nil {
		return err
	}

	if err := parts.newBoards(ctx, config.Boards, logger); err != nil {
		return err
	}

	if err := parts.newComponents(ctx, config.Components, robot); err != nil {
		return err
	}

	for _, f := range config.Functions {
		parts.addFunction(f.Name)
	}

	return nil
}

// processModifiedConfig ingests a given config and constructs all constituent parts.
func (parts *robotParts) processModifiedConfig(
	ctx context.Context,
	config *config.ModifiedConfigDiff,
	robot *mutableRobot,
	logger golog.Logger,
) error {
	if err := parts.newProcesses(ctx, config.Processes); err != nil {
		return err
	}

	if err := parts.newRemotes(ctx, config.Remotes, logger); err != nil {
		return err
	}

	if err := parts.newBoardsModified(ctx, config.Boards, logger); err != nil {
		return err
	}

	if err := parts.newComponents(ctx, config.Components, robot); err != nil {
		return err
	}

	for _, f := range config.Functions {
		parts.addFunction(f.Name)
	}

	return nil
}

// newProcesses constructs all processes defined.
func (parts *robotParts) newProcesses(ctx context.Context, processes []pexec.ProcessConfig) error {
	for _, procConf := range processes {
		if _, err := parts.processManager.AddProcessFromConfig(ctx, procConf); err != nil {
			return err
		}
	}
	return parts.processManager.Start(ctx)
}

// newRemotes constructs all remotes defined and integrates their parts in.
func (parts *robotParts) newRemotes(ctx context.Context, remotes []config.Remote, logger golog.Logger) error {
	for _, config := range remotes {
		robotClient, err := client.NewClient(ctx, config.Address, logger)
		if err != nil {
			return errors.Errorf("couldn't connect to robot remote (%s): %w", config.Address, err)
		}

		configCopy := config
		parts.addRemote(newRemoteRobot(robotClient, configCopy), configCopy)
	}
	return nil
}

// newBoards constructs all boards defined.
func (parts *robotParts) newBoards(ctx context.Context, boards []board.Config, logger golog.Logger) error {
	for _, c := range boards {
		b, err := board.NewBoard(ctx, c, logger)
		if err != nil {
			return err
		}
		parts.AddBoard(b, c)
	}
	return nil
}

// newBoardsModified constructs all boards defined from modifications.
func (parts *robotParts) newBoardsModified(ctx context.Context, boardDiffs map[string]board.ConfigDiff, logger golog.Logger) error {
	for _, diff := range boardDiffs {
		config, err := diff.ToConfig()
		if err != nil {
			return err
		}
		b, err := board.NewBoard(ctx, *config, logger)
		if err != nil {
			return err
		}
		parts.AddBoard(b, *config)
	}
	return nil
}

// newComponents constructs all components defined.
func (parts *robotParts) newComponents(ctx context.Context, components []config.Component, r *mutableRobot) error {
	for _, c := range components {
		switch c.Type {
		case config.ComponentTypeProvider:
			p, err := r.newProvider(ctx, c)
			if err != nil {
				return err
			}
			parts.AddProvider(p, c)
		}
	}

	for _, c := range components {
		switch c.Type {
		case config.ComponentTypeProvider:
			// handled above
		case config.ComponentTypeBase:
			b, err := r.newBase(ctx, c)
			if err != nil {
				return err
			}
			parts.AddBase(b, c)
		case config.ComponentTypeArm:
			a, err := r.newArm(ctx, c)
			if err != nil {
				return err
			}
			parts.AddArm(a, c)
		case config.ComponentTypeGripper:
			g, err := r.newGripper(ctx, c)
			if err != nil {
				return err
			}
			parts.AddGripper(g, c)
		case config.ComponentTypeCamera:
			camera, err := r.newCamera(ctx, c)
			if err != nil {
				return err
			}
			parts.AddCamera(camera, c)
		case config.ComponentTypeLidar:
			lidar, err := r.newLidar(ctx, c)
			if err != nil {
				return err
			}
			parts.AddLidar(lidar, c)
		case config.ComponentTypeSensor:
			if c.SubType == "" {
				return errors.New("sensor component requires subtype")
			}
			sensorDevice, err := r.newSensor(ctx, c, sensor.Type(c.SubType))
			if err != nil {
				return err
			}
			parts.AddSensor(sensorDevice, c)
		default:
			return errors.Errorf("unknown component type: %v", c.Type)
		}
	}

	return nil
}

// RemoteByName returns the given remote robot by name, if it exists;
// returns nil otherwise.
func (parts *robotParts) RemoteByName(name string) (robot.Robot, bool) {
	part, ok := parts.remotes[name]
	if ok {
		return part, true
	}
	for _, remote := range parts.remotes {
		part, ok := remote.RemoteByName(name)
		if ok {
			return part, true
		}
	}
	return nil, false
}

// BoardByName returns the given board by name, if it exists;
// returns nil otherwise.
func (parts *robotParts) BoardByName(name string) (board.Board, bool) {
	part, ok := parts.boards[name]
	if ok {
		return part, true
	}
	for _, remote := range parts.remotes {
		part, ok := remote.BoardByName(name)
		if ok {
			return part, true
		}
	}
	return nil, false
}

// ArmByName returns the given arm by name, if it exists;
// returns nil otherwise.
func (parts *robotParts) ArmByName(name string) (arm.Arm, bool) {
	part, ok := parts.arms[name]
	if ok {
		return part, true
	}
	for _, remote := range parts.remotes {
		part, ok := remote.ArmByName(name)
		if ok {
			return part, true
		}
	}
	return nil, false
}

// BaseByName returns the given base by name, if it exists;
// returns nil otherwise.
func (parts *robotParts) BaseByName(name string) (base.Base, bool) {
	part, ok := parts.bases[name]
	if ok {
		return part, true
	}
	for _, remote := range parts.remotes {
		part, ok := remote.BaseByName(name)
		if ok {
			return part, true
		}
	}
	return nil, false
}

// GripperByName returns the given gripper by name, if it exists;
// returns nil otherwise.
func (parts *robotParts) GripperByName(name string) (gripper.Gripper, bool) {
	part, ok := parts.grippers[name]
	if ok {
		return part, true
	}
	for _, remote := range parts.remotes {
		part, ok := remote.GripperByName(name)
		if ok {
			return part, true
		}
	}
	return nil, false
}

// CameraByName returns the given camera by name, if it exists;
// returns nil otherwise.
func (parts *robotParts) CameraByName(name string) (camera.Camera, bool) {
	part, ok := parts.cameras[name]
	if ok {
		return part, true
	}
	for _, remote := range parts.remotes {
		part, ok := remote.CameraByName(name)
		if ok {
			return part, true
		}
	}
	return nil, false
}

// LidarByName returns the given lidar by name, if it exists;
// returns nil otherwise.
func (parts *robotParts) LidarByName(name string) (lidar.Lidar, bool) {
	part, ok := parts.lidars[name]
	if ok {
		return part, true
	}
	for _, remote := range parts.remotes {
		part, ok := remote.LidarByName(name)
		if ok {
			return part, true
		}
	}
	return nil, false
}

// SensorByName returns the given sensor by name, if it exists;
// returns nil otherwise.
func (parts *robotParts) SensorByName(name string) (sensor.Sensor, bool) {
	part, ok := parts.sensors[name]
	if ok {
		return part, true
	}
	for _, remote := range parts.remotes {
		part, ok := remote.SensorByName(name)
		if ok {
			return part, true
		}
	}
	return nil, false
}

// ProviderByName returns the given provider by name, if it exists;
// returns nil otherwise.
func (parts *robotParts) ProviderByName(name string) (robot.Provider, bool) {
	part, ok := parts.providers[name]
	if ok {
		return part, true
	}
	for _, remote := range parts.remotes {
		part, ok := remote.ProviderByName(name)
		if ok {
			return part, true
		}
	}
	return nil, false
}

// PartsMergeResult is the result of merging in parts together.
type PartsMergeResult struct {
	ReplacedProcesses []pexec.ManagedProcess
}

// Process integrates the results into the given parts.
func (result *PartsMergeResult) Process(parts *robotParts) error {
	for _, proc := range result.ReplacedProcesses {
		if replaced, err := parts.processManager.AddProcess(context.Background(), proc, false); err != nil {
			return err
		} else if replaced != nil {
			return errors.Errorf("unexpected process replacement %v", replaced)
		}
	}
	return nil
}

// MergeAdd merges in the given added parts and returns results for
// later processing.
func (parts *robotParts) MergeAdd(toAdd *robotParts) (*PartsMergeResult, error) {
	if len(toAdd.remotes) != 0 {
		if parts.remotes == nil {
			parts.remotes = make(map[string]*remoteRobot, len(toAdd.remotes))
		}
		for k, v := range toAdd.remotes {
			parts.remotes[k] = v
		}
	}

	if len(toAdd.boards) != 0 {
		if parts.boards == nil {
			parts.boards = make(map[string]*proxyBoard, len(toAdd.boards))
		}
		for k, v := range toAdd.boards {
			parts.boards[k] = v
		}
	}

	if len(toAdd.arms) != 0 {
		if parts.arms == nil {
			parts.arms = make(map[string]*proxyArm, len(toAdd.arms))
		}
		for k, v := range toAdd.arms {
			parts.arms[k] = v
		}
	}

	if len(toAdd.grippers) != 0 {
		if parts.grippers == nil {
			parts.grippers = make(map[string]*proxyGripper, len(toAdd.grippers))
		}
		for k, v := range toAdd.grippers {
			parts.grippers[k] = v
		}
	}

	if len(toAdd.cameras) != 0 {
		if parts.cameras == nil {
			parts.cameras = make(map[string]*proxyCamera, len(toAdd.cameras))
		}
		for k, v := range toAdd.cameras {
			parts.cameras[k] = v
		}
	}

	if len(toAdd.lidars) != 0 {
		if parts.lidars == nil {
			parts.lidars = make(map[string]*proxyLidar, len(toAdd.lidars))
		}
		for k, v := range toAdd.lidars {
			parts.lidars[k] = v
		}
	}

	if len(toAdd.bases) != 0 {
		if parts.bases == nil {
			parts.bases = make(map[string]*proxyBase, len(toAdd.bases))
		}
		for k, v := range toAdd.bases {
			parts.bases[k] = v
		}
	}

	if len(toAdd.sensors) != 0 {
		if parts.sensors == nil {
			parts.sensors = make(map[string]sensor.Sensor, len(toAdd.sensors))
		}
		for k, v := range toAdd.sensors {
			parts.sensors[k] = v
		}
	}

	if len(toAdd.functions) != 0 {
		if parts.functions == nil {
			parts.functions = make(map[string]struct{}, len(toAdd.functions))
		}
		for k, v := range toAdd.functions {
			parts.functions[k] = v
		}
	}

	if len(toAdd.providers) != 0 {
		if parts.providers == nil {
			parts.providers = make(map[string]*proxyProvider, len(toAdd.providers))
		}
		for k, v := range toAdd.providers {
			parts.providers[k] = v
		}
	}

	var result PartsMergeResult
	if toAdd.processManager != nil {
		// assume parts.processManager is non-nil
		replaced, err := pexec.MergeAddProcessManagers(parts.processManager, toAdd.processManager)
		if err != nil {
			return nil, err
		}
		result.ReplacedProcesses = replaced
	}

	return &result, nil
}

// MergeModify merges in the given modified parts and returns results for
// later processing.
func (parts *robotParts) MergeModify(ctx context.Context, toModify *robotParts, diff *config.Diff) (*PartsMergeResult, error) {
	var result PartsMergeResult
	if toModify.processManager != nil {
		// assume parts.processManager is non-nil
		// adding also replaces here
		replaced, err := pexec.MergeAddProcessManagers(parts.processManager, toModify.processManager)
		if err != nil {
			return nil, err
		}
		result.ReplacedProcesses = replaced
	}

	// this is the point of no return during reconfiguration

	if len(toModify.remotes) != 0 {
		for k, v := range toModify.remotes {
			old, ok := parts.remotes[k]
			if !ok {
				// should not happen
				continue
			}
			old.replace(v)
		}
	}

	if len(toModify.boards) != 0 {
		for k, v := range toModify.boards {
			old, ok := parts.boards[k]
			if !ok {
				// should not happen
				continue
			}
			old.merge(v, diff.Modified.Boards[k])
		}
	}

	if len(toModify.arms) != 0 {
		for k, v := range toModify.arms {
			old, ok := parts.arms[k]
			if !ok {
				// should not happen
				continue
			}
			old.replace(v)
		}
	}

	if len(toModify.grippers) != 0 {
		for k, v := range toModify.grippers {
			old, ok := parts.grippers[k]
			if !ok {
				// should not happen
				continue
			}
			old.replace(v)
		}
	}

	if len(toModify.cameras) != 0 {
		for k, v := range toModify.cameras {
			old, ok := parts.cameras[k]
			if !ok {
				// should not happen
				continue
			}
			old.replace(v)
		}
	}

	if len(toModify.lidars) != 0 {
		for k, v := range toModify.lidars {
			old, ok := parts.lidars[k]
			if !ok {
				// should not happen
				continue
			}
			old.replace(v)
		}
	}

	if len(toModify.bases) != 0 {
		for k, v := range toModify.bases {
			old, ok := parts.bases[k]
			if !ok {
				// should not happen
				continue
			}
			old.replace(v)
		}
	}

	if len(toModify.sensors) != 0 {
		for k, v := range toModify.sensors {
			old, ok := parts.sensors[k]
			if !ok {
				// should not happen
				continue
			}
			old.(interface{ replace(newSensor sensor.Sensor) }).replace(v)
		}
	}

	if len(toModify.providers) != 0 {
		for k, v := range toModify.providers {
			old, ok := parts.providers[k]
			if !ok {
				// should not happen
				continue
			}
			old.replace(v)
		}
	}

	return &result, nil
}

// MergeRemove merges in the given removed parts but does no work
// to stop the individual parts.
func (parts *robotParts) MergeRemove(toRemove *robotParts) {
	if len(toRemove.remotes) != 0 {
		for k := range toRemove.remotes {
			delete(parts.remotes, k)
		}
	}

	if len(toRemove.boards) != 0 {
		for k := range toRemove.boards {
			delete(parts.boards, k)
		}
	}

	if len(toRemove.arms) != 0 {
		for k := range toRemove.arms {
			delete(parts.arms, k)
		}
	}

	if len(toRemove.grippers) != 0 {
		for k := range toRemove.grippers {
			delete(parts.grippers, k)
		}
	}

	if len(toRemove.cameras) != 0 {
		for k := range toRemove.cameras {
			delete(parts.cameras, k)
		}
	}

	if len(toRemove.lidars) != 0 {
		for k := range toRemove.lidars {
			delete(parts.lidars, k)
		}
	}

	if len(toRemove.bases) != 0 {
		for k := range toRemove.bases {
			delete(parts.bases, k)
		}
	}

	if len(toRemove.sensors) != 0 {
		for k := range toRemove.sensors {
			delete(parts.sensors, k)
		}
	}

	if len(toRemove.functions) != 0 {
		for k := range toRemove.functions {
			delete(parts.functions, k)
		}
	}

	if len(toRemove.providers) != 0 {
		for k := range toRemove.providers {
			delete(parts.providers, k)
		}
	}

	if toRemove.processManager != nil {
		// assume parts.processManager is non-nil
		// ignoring result as we will filter out the processes to remove and stop elsewhere
		pexec.MergeRemoveProcessManagers(parts.processManager, toRemove.processManager)
	}
}

// FilterFromConfig returns a shallow copy of the parts reflecting
// a given config.
func (parts *robotParts) FilterFromConfig(conf *config.Config, logger golog.Logger) (*robotParts, error) {
	filtered := newRobotParts(logger)

	for _, conf := range conf.Processes {
		proc, ok := parts.processManager.ProcessByID(conf.ID)
		if !ok {
			continue
		}
		if _, err := filtered.processManager.AddProcess(context.Background(), proc, false); err != nil {
			return nil, err
		}
	}

	for _, conf := range conf.Remotes {
		part, ok := parts.remotes[conf.Name]
		if !ok {
			continue
		}
		filtered.addRemote(part, conf)
	}

	for _, conf := range conf.Boards {
		part, ok := parts.BoardByName(conf.Name)
		if !ok {
			continue
		}
		filtered.AddBoard(part, conf)
	}

	for _, compConf := range conf.Components {
		switch compConf.Type {
		case config.ComponentTypeProvider:
			part, ok := parts.ProviderByName(compConf.Name)
			if !ok {
				continue
			}
			filtered.AddProvider(part, compConf)
		case config.ComponentTypeBase:
			part, ok := parts.BaseByName(compConf.Name)
			if !ok {
				continue
			}
			filtered.AddBase(part, compConf)
		case config.ComponentTypeArm:
			part, ok := parts.ArmByName(compConf.Name)
			if !ok {
				continue
			}
			filtered.AddArm(part, compConf)
		case config.ComponentTypeGripper:
			part, ok := parts.GripperByName(compConf.Name)
			if !ok {
				continue
			}
			filtered.AddGripper(part, compConf)
		case config.ComponentTypeCamera:
			part, ok := parts.CameraByName(compConf.Name)
			if !ok {
				continue
			}
			filtered.AddCamera(part, compConf)
		case config.ComponentTypeLidar:
			part, ok := parts.LidarByName(compConf.Name)
			if !ok {
				continue
			}
			filtered.AddLidar(part, compConf)
		case config.ComponentTypeSensor:
			part, ok := parts.SensorByName(compConf.Name)
			if !ok {
				continue
			}
			filtered.AddSensor(part, compConf)
		default:
			return nil, errors.Errorf("unknown component type: %v", compConf.Type)
		}
	}

	for _, conf := range conf.Functions {
		_, ok := parts.functions[conf.Name]
		if !ok {
			continue
		}
		filtered.addFunction(conf.Name)
	}

	return filtered, nil
}
