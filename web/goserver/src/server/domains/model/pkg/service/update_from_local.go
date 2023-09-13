package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	fp "path/filepath"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"gopkg.in/yaml.v2"

	buildFindOne "server/db/pkg/handler/build/find_one"
	buildInsertOne "server/db/pkg/handler/build/insert_one"
	modelUpdateUpsert "server/db/pkg/handler/model/update_upsert"
	problemFindOne "server/db/pkg/handler/problem/find_one"
	t "server/db/pkg/types"
	buildStatus "server/db/pkg/types/build/status"
	statusModelEvaluate "server/db/pkg/types/status/model/evaluate"
	statusModelTrain "server/db/pkg/types/status/model/train"
	kitendpoint "server/kit/endpoint"
	u "server/kit/utils"
	uFiles "server/kit/utils/basic/files"
)

type Basic struct {
	BatchSize        int     `yaml:"batch_size"`
	BaseLearningRate float64 `yaml:"base_learning_rate"`
	Epochs           int     `yaml:"epochs"`
}

type HyperParameters struct {
	Basic Basic `yaml:"basic"`
}

type ModelYml struct {
	Class           string          `yaml:"domain"`
	Name            string          `yaml:"name"`
	Problem         string          `yaml:"problem"`
	Dependencies    []t.Dependency  `yaml:"dependencies"`
	Metrics         []t.Metric      `yaml:"metrics"`
	GpuNum          int             `yaml:"gpu_num"`
	Config          string          `yaml:"config"`
	HyperParameters HyperParameters `yaml:"hyper_parameters"`
}

type UpdateFromLocalRequestData struct {
	Path string `json:"path"`
}

func (s *basicModelService) UpdateFromLocal(ctx context.Context, req UpdateFromLocalRequestData) chan kitendpoint.Response {
	responseChan := make(chan kitendpoint.Response)
	go func() {
		templateYaml := getTemplateYaml(req.Path)
		problem, err := s.getProblem(ctx, templateYaml.Problem)
		if err != nil {
			responseChan <- kitendpoint.Response{Data: nil, Err: kitendpoint.Error{Code: 1}, IsLast: true}
			return
		}
		defaultBuild := s.getDefaultBuild(problem.Id)
		model := s.prepareModel(templateYaml, defaultBuild.Id, problem)
		copyModelFiles(fp.Dir(req.Path), model.Dir, req.Path, templateYaml)
		model = s.updateCreateModel(model)
		responseChan <- kitendpoint.Response{Data: model, Err: kitendpoint.Error{Code: 0}, IsLast: true}
	}()
	return responseChan
}

func copyModelFiles(from, to, modelTemplatePath string, modelYml ModelYml) {
	copyConfig(from, to, modelYml)
	copyModulesYaml(from, to)
	copyDependencies(from, to, modelYml)
	saveMetrics(to, modelYml)
	copyTemplateYaml(modelTemplatePath, to)
}

func copyConfig(from, to string, modelYml ModelYml) {
	if err := copyFiles(fp.Join(from, modelYml.Config), fp.Join(to, modelYml.Config)); err != nil {
		log.Println("update_from_local.copyDependencies.copyFiles(fp.Join(from, modelYml.Config), fp.Join(to, modelYml.Config))", err)
	}
}

func copyModulesYaml(from, to string) {
	modulesYaml := "modules.yaml"
	if err := copyFiles(fp.Join(from, modulesYaml), fp.Join(to, modulesYaml)); err != nil {
		log.Println("update_from_local.copyDependencies.copyFiles(fp.Join(from,modulesYaml), fp.Join(to, modulesYaml))", err)
	}
}

func copyTemplateYaml(from, to string) string {
	templateYamlPath := fp.Join(to, "template.yaml")
	if err := copyFiles(from, templateYamlPath); err != nil {
		log.Println("update_from_local.copyDependencies.copyFiles(fp.Join(from, modelYml.Config), fp.Join(to, modelYml.Config))", err)
	}
	return templateYamlPath
}

func copyDependencies(from, to string, modelYml ModelYml) {
	for _, d := range modelYml.Dependencies {
		toPath := fp.Join(to, d.Destination)
		if isValidUrl(d.Source) {
			if err := downloadWithCheck(d.Source, toPath, d.Sha256, d.Size); err != nil {
				log.Println("update_from_local.copyDependencies.downloadWithCheck(d.Source, d.Destination, d.Sha256, d.Size)", err)
			}
		} else {
			if err := copyFiles(fp.Join(from, d.Source), toPath); err != nil {
				log.Println("update_from_local.copyDependencies.copyFiles(fp.Join(from, d.Source), fp.Join(to, d.Destination))", err)
			}
		}
	}
}

func saveMetrics(to string, modelYml ModelYml) {
	type MetricsYaml struct {
		Metrics []t.Metric `yaml:"metrics"`
	}
	metricsPath := fp.Join(to, "_default", "metrics.yaml")
	if err := os.MkdirAll(fp.Dir(metricsPath), 0777); err != nil {
		log.Println("saveMetrics.os.MkdirAll(fp.Dir(metricsPath), 0777)", err)
	}
	f, err := os.Create(metricsPath)
	if err != nil {
		log.Println("saveMetrics.os.Create(metricsPath)", err)
	}
	metrics, err := yaml.Marshal(MetricsYaml{modelYml.Metrics})
	if err != nil {
		log.Println("saveMetrics.yaml.Marshal(modelYml.Metrics)", err)
	}
	_, err = f.Write(metrics)
	if err != nil {
		log.Println("saveMetrics.f.Write(metrics)", err)
	}
	if err := f.Sync(); err != nil {
		log.Println("saveMetrics.f.Sync()", err)
	}
}

func copyFiles(from, to string) error {
	si, err := os.Stat(from)
	if err != nil {
		log.Println("update_from_local.copyFiles.os.Stat(from)", err)
		return err
	}
	if si.IsDir() {
		if err := uFiles.CopyDir(from, to); err != nil {
			log.Println("update_from_local.copyFiles.uFiles.CopyDir(from, to)", err)
		}
	} else {
		if _, err := uFiles.Copy(from, to); err != nil {
			log.Println("update_from_local.copyFiles.uFiles.Copy(from, to)", err)
			return err
		}
	}
	return nil
}

func isValidUrl(toTest string) bool {
	_, err := url.ParseRequestURI(toTest)
	if err != nil {
		return false
	}

	u, err := url.Parse(toTest)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	return true
}

func (s *basicModelService) prepareModel(modelYml ModelYml, buildId primitive.ObjectID, problem t.Problem) t.Model {
	modelFolderName := u.StringToFolderName(modelYml.Name)
	dir := fp.Join(problem.Dir, modelFolderName)
	metrics := make(map[string][]t.Metric)
	metrics[buildId.Hex()] = modelYml.Metrics
	evaluates := make(map[string]t.Evaluate)
	evaluates[buildId.Hex()] = t.Evaluate{
		Metrics: modelYml.Metrics,
		Status:  statusModelEvaluate.Default,
	}
	model := t.Model{
		BatchSize:       modelYml.HyperParameters.Basic.BatchSize,
		ConfigPath:      fp.Join(dir, modelYml.Config),
		ProblemId:       problem.Id,
		Description:     "",
		Dir:             dir,
		Epochs:          modelYml.HyperParameters.Basic.Epochs,
		Evaluates:       evaluates,
		ModulesYamlPath: fp.Join(dir, "modules.yaml"),
		Name:            modelYml.Name,
		Scripts: t.Scripts{
			Train: fp.Join(dir, "train.py"),
			Eval:  fp.Join(dir, "eval.py"),
		},
		SnapshotPath:   fp.Join(dir, "snapshot.pth"),
		Status:         statusModelTrain.Default,
		TemplatePath:   fp.Join(dir, "template.yaml"),
		TrainingGpuNum: modelYml.GpuNum,
	}
	log.Println("Epochs:", modelYml.HyperParameters.Basic.Epochs, model.Epochs)
	return model
}

func getTemplateYaml(path string) (modelYml ModelYml) {
	yamlFile, err := ioutil.ReadFile(path)
	if err != nil {
		log.Println("ReadFile", err)
	}
	err = yaml.Unmarshal(yamlFile, &modelYml)

	if err != nil {
		log.Println("Unmarshal", err)
	}
	log.Println("Model BatchSize", modelYml.HyperParameters.Basic.BatchSize)
	return modelYml
}

func (s *basicModelService) getProblem(ctx context.Context, title string) (t.Problem, error) {
	problemResp := <-problemFindOne.Send(
		ctx,
		s.Conn,
		problemFindOne.RequestData{
			Title: title,
		},
	)
	var err error = nil
	if problemResp.Err.Code > 0 {
		err = fmt.Errorf(problemResp.Err.Message)
	}
	return problemResp.Data.(problemFindOne.ResponseData), err
}

func downloadWithCheck(url, dst, sha256 string, size int) error {
	for i := 0; i < 10; i++ {
		nBytes, err := u.DownloadFile(url, dst)
		if err != nil {
			log.Println("downloadWithCheck.DownloadFile", err)
			continue
		}
		log.Println(dst, nBytes)
		if nBytes != int64(size) {
			log.Println("downloadWithCheck.WrongSize", err)
			err = errors.New("wrong size")
			continue
		}
		dstSha265 := getSha265(dst)
		if dstSha265 != sha256 {
			log.Println("downloadWithCheck.WrongSha", err)
			err = errors.New("wrong sha")
			continue
		}
		break
	}
	return nil

}

func getSha265(path string) string {
	f, err := os.Open(path)
	if err != nil {
		log.Println("getSha265.os.Open(path)", err)
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		log.Println("getSha265.io.Copy(h, f)", err)
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *basicModelService) updateCreateModel(model t.Model) t.Model {
	log.Println("updateCreateModel.Epochs", model.Epochs)
	modelResp := <-modelUpdateUpsert.Send(
		context.TODO(),
		s.Conn,
		modelUpdateUpsert.RequestData{
			ConfigPath:      model.ConfigPath,
			BatchSize:       model.BatchSize,
			Description:     model.Description,
			Dir:             model.Dir,
			Epochs:          model.Epochs,
			Evaluates:       model.Evaluates,
			Framework:       model.Framework,
			ModulesYamlPath: model.ModulesYamlPath,
			Name:            model.Name,
			Scripts:         model.Scripts,
			SnapshotPath:    model.SnapshotPath,
			Status:          model.Status,
			ProblemId:       model.ProblemId,
			TemplatePath:    model.TemplatePath,
			TrainingGpuNum:  model.TrainingGpuNum,
		},
	)
	return modelResp.Data.(modelUpdateUpsert.ResponseData)
}

func (s *basicModelService) getDefaultBuild(problemId primitive.ObjectID) (result t.Build) {
	buildFindOneResp := <-buildFindOne.Send(
		context.TODO(),
		s.Conn,
		buildFindOne.RequestData{
			ProblemId: problemId,
			Name:      "default",
		},
	)
	result = buildFindOneResp.Data.(buildFindOne.ResponseData)
	if result.Id.IsZero() {
		buildInsertOneResp := <-buildInsertOne.Send(
			context.TODO(),
			s.Conn,
			buildInsertOne.RequestData{
				ProblemId: problemId,
				Name:      "default",
				Status:    buildStatus.Default,
			},
		)
		result = buildInsertOneResp.Data.(buildInsertOne.ResponseData)
	}
	return result
}
