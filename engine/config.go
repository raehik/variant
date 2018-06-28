package engine

import (
	"fmt"
	"reflect"

	log "github.com/Sirupsen/logrus"
	"github.com/juju/errors"
	"github.com/mitchellh/mapstructure"

	"github.com/mumoshu/variant/api/step"
	"github.com/mumoshu/variant/util/maputil"
)

type TaskConfig struct {
	Name        string        `yaml:"name,omitempty"`
	Description string        `yaml:"description,omitempty"`
	Inputs      []*Input      `yaml:"inputs,omitempty"`
	TaskConfigs []*TaskConfig `yaml:"tasks,omitempty"`
	Script      string        `yaml:"script,omitempty"`
	Steps       []step.Step   `yaml:"steps,omitempty"`
	Autoenv     bool          `yaml:"autoenv,omitempty"`
	Autodir     bool          `yaml:"autodir,omitempty"`
	Interactive bool          `yaml:"interactive,omitempty"`
}

type TaskConfigV1 struct {
	Name        string                        `yaml:"name,omitempty"`
	Description string                        `yaml:"description,omitempty"`
	Inputs      []*Input                      `yaml:"inputs,omitempty"`
	TaskConfigs []*TaskConfig                 `yaml:"tasks,omitempty"`
	Script      string                        `yaml:"script,omitempty"`
	StepConfigs []map[interface{}]interface{} `yaml:"steps,omitempty"`
	Autoenv     bool                          `yaml:"autoenv,omitempty"`
	Autodir     bool                          `yaml:"autodir,omitempty"`
	Interactive bool                          `yaml:"interactive,omitempty"`
}

type TaskConfigV2 struct {
	Description string                        `yaml:"description,omitempty"`
	Inputs      []*Input                      `yaml:"inputs,omitempty"`
	TaskConfigs map[string]*TaskConfig        `yaml:"tasks,omitempty"`
	Script      string                        `yaml:"script,omitempty"`
	StepConfigs []map[interface{}]interface{} `yaml:"steps,omitempty"`
	Autoenv     bool                          `yaml:"autoenv,omitempty"`
	Autodir     bool                          `yaml:"autodir,omitempty"`
	Interactive bool                          `yaml:"interactive,omitempty"`
}

func (t *TaskConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	v3 := map[string]interface{}{}
	v3err := unmarshal(&v3)

	log.Debugf("Unmarshalling: %v", v3)

	log.Debugf("Trying to parse v1 format")

	v1 := TaskConfigV1{
		Autoenv:     false,
		Autodir:     false,
		Inputs:      []*Input{},
		TaskConfigs: []*TaskConfig{},
		StepConfigs: []map[interface{}]interface{}{},
	}

	err := unmarshal(&v1)

	if v1.Name == "" && len(v1.TaskConfigs) == 0 {
		e := fmt.Errorf("Not v1 format: Both `name` and `tasks` are empty")
		log.Debugf("%s", e)
		err = e
	}

	if err == nil {
		t.Name = v1.Name
		t.Description = v1.Description
		t.Inputs = v1.Inputs
		t.TaskConfigs = v1.TaskConfigs
		t.Script = v1.Script
		t.Autoenv = v1.Autoenv
		t.Autodir = v1.Autodir
		t.Interactive = v1.Interactive
		steps, err := readStepsFromStepConfigs(v1.Script, v1.StepConfigs)
		if err != nil {
			return errors.Annotatef(err, "Error while reading v1 config")
		}
		t.Steps = steps
	}

	var v2 *TaskConfigV2

	if err != nil {
		log.Debugf("Trying to parse v2 format")
		v2 = &TaskConfigV2{
			Autoenv:     false,
			Autodir:     false,
			Interactive: false,
			Inputs:      []*Input{},
			TaskConfigs: map[string]*TaskConfig{},
			StepConfigs: []map[interface{}]interface{}{},
		}

		err = unmarshal(&v2)

		if len(v2.TaskConfigs) == 0 && v2.Script == "" && len(v2.StepConfigs) == 0 {
			e := fmt.Errorf("Not v2 format: `tasks`, `script`, `steps` are missing.")
			log.Debugf("%s", e)
			err = e
		}

		if err == nil {
			t.Description = v2.Description
			t.Inputs = v2.Inputs
			t.TaskConfigs = TransformV2FlowConfigMapToArray(v2.TaskConfigs)
			steps, err := readStepsFromStepConfigs(v2.Script, v2.StepConfigs)
			if err != nil {
				return errors.Annotatef(err, "Error while reading v2 config")
			}
			t.Steps = steps
			t.Script = v2.Script
			t.Autoenv = v2.Autoenv
			t.Autodir = v2.Autodir
			t.Interactive = v2.Interactive
		}

	}

	if err != nil {
		log.Debugf("Trying to parse v3 format")

		if v3err != nil {
			return errors.Annotate(v3err, "Failed to unmarshal as a map.")
		}

		if v3["tasks"] != nil {
			rawTasks, ok := v3["tasks"].(map[interface{}]interface{})
			if !ok {
				return fmt.Errorf("Not a map[interface{}]interface{}: v3[\"tasks\"]'s type: %s, value: %s", reflect.TypeOf(v3["tasks"]), v3["tasks"])
			}
			tasks, err := maputil.CastKeysToStrings(rawTasks)
			if err != nil {
				return errors.Annotate(err, "Failed to unmarshal as a map[string]interface{}")
			}
			t.Autoenv = false
			t.Autodir = false
			t.Inputs = []*Input{}

			t.TaskConfigs = TransformV3FlowConfigMapToArray(tasks)

			return nil
		}
	}

	return errors.Trace(err)
}

func (t *TaskConfig) CopyTo(other *TaskConfig) {
	other.Description = t.Description
	other.Inputs = t.Inputs
	other.TaskConfigs = t.TaskConfigs
	other.Steps = t.Steps
	other.Script = t.Script
	other.Autoenv = t.Autoenv
	other.Autodir = t.Autodir
	other.Interactive = t.Interactive
}

func TransformV2FlowConfigMapToArray(v2 map[string]*TaskConfig) []*TaskConfig {
	result := []*TaskConfig{}
	for name, t2 := range v2 {
		t := &TaskConfig{}

		t.Name = name
		t2.CopyTo(t)

		result = append(result, t)
	}
	return result
}

var stepLoaders []step.StepLoader

func Register(stepLoader step.StepLoader) {
	stepLoaders = append(stepLoaders, stepLoader)
}

func init() {
	stepLoaders = []step.StepLoader{}
}

type stepLoadingContextImpl struct{}

func (s stepLoadingContextImpl) LoadStep(config step.StepConfig) (step.Step, error) {
	step, err := LoadStep(config)

	return step, err
}

func LoadStep(config step.StepConfig) (step.Step, error) {
	var lastError error

	lastError = nil

	context := stepLoadingContextImpl{}
	for _, loader := range stepLoaders {
		var s step.Step
		s, lastError = loader.LoadStep(config, context)

		log.WithField("step", s).Debugf("step loaded")

		if lastError == nil {
			return s, nil
		}
	}
	return nil, errors.Annotatef(lastError, "all loader failed to load step")
}

func readStepsFromStepConfigs(script string, stepConfigs []map[interface{}]interface{}) ([]step.Step, error) {
	result := []step.Step{}

	if script != "" {
		if len(stepConfigs) > 0 {
			return nil, fmt.Errorf("both script and steps exist.")
		}

		s, err := LoadStep(step.NewStepConfig(map[string]interface{}{
			"name":   "script",
			"script": script,
		}))

		if err != nil {
			log.Panicf("step failed to load: %v", err)
		}

		result = []step.Step{s}
	} else {
		for i, stepConfig := range stepConfigs {
			defaultName := fmt.Sprintf("step-%d", i+1)

			if stepConfig["name"] == "" || stepConfig["name"] == nil {
				stepConfig["name"] = defaultName
			}

			converted, castErr := maputil.CastKeysToStrings(stepConfig)

			if castErr != nil {
				panic(castErr)
			}

			s, err := LoadStep(step.NewStepConfig(converted))

			if err != nil {
				return nil, errors.Annotatef(err, "Error reading step[%d]")
			}

			result = append(result, s)
		}
	}

	return result, nil
}

func TransformV3FlowConfigMapToArray(v3 map[string]interface{}) []*TaskConfig {
	result := []*TaskConfig{}
	for k, v := range v3 {
		t := &TaskConfig{
			Autoenv:     false,
			Autodir:     false,
			Inputs:      []*Input{},
			TaskConfigs: []*TaskConfig{},
		}

		log.Debugf("Arrived %s: %v", k, v)
		log.Debugf("Type of value: %v", reflect.TypeOf(v))

		t.Name = k

		var err error

		i2i, ok := v.(map[interface{}]interface{})

		if !ok {
			panic(fmt.Errorf("Not a map[interface{}]interface{}: %s", v))
		}

		s2i, err := maputil.CastKeysToStrings(i2i)

		if err != nil {
			panic(errors.Annotate(err, "Unexpected structure"))
		}

		leaf := s2i["script"] != nil

		if !leaf {
			t.TaskConfigs = TransformV3FlowConfigMapToArray(s2i)
		} else {
			log.Debugf("Not a nested map")
			err = mapstructure.Decode(s2i, t)
			if err != nil {
				panic(errors.Trace(err))
			}
			log.Debugf("Loaded %v", t)
		}

		result = append(result, t)
	}
	return result
}
