/*

Copyright (C) 2017-2018  Ettore Di Giacinto <mudler@gentoo.org>
Credits goes also to Gogs authors, some code portions and re-implemented design
are also coming from the Gogs project, which is using the go-macaron framework
and was really source of ispiration. Kudos to them!

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

*/

package agenttasks

import (
	"errors"
	"net/url"
	"path"
	"strings"
	"time"

	setting "github.com/MottainaiCI/mottainai-server/pkg/settings"
	"github.com/MottainaiCI/mottainai-server/pkg/utils"
	docker "github.com/fsouza/go-dockerclient"
)

type DockerExecutor struct {
	*TaskExecutor
	DockerClient *docker.Client
}

func NewDockerExecutor() *DockerExecutor {
	return &DockerExecutor{TaskExecutor: &TaskExecutor{Context: NewExecutorContext()}}
}

func (e *DockerExecutor) Prune() {
	e.DockerClient.PruneContainers(docker.PruneContainersOptions{})
	e.DockerClient.PruneImages(docker.PruneImagesOptions{})
	e.DockerClient.PruneVolumes(docker.PruneVolumesOptions{})
	e.DockerClient.PruneNetworks(docker.PruneNetworksOptions{})
}

func (d *DockerExecutor) Setup(docID string) error {
	d.TaskExecutor.Setup(docID)
	docker_client, err := docker.NewClient(setting.Configuration.DockerEndpoint)
	if err != nil {
		return (errors.New("Endpoint:" + setting.Configuration.DockerEndpoint + " Error: " + err.Error()))
	}
	d.DockerClient = docker_client
	return nil
}

func purgeImageName(image string) string {
	return strings.Replace(image, "/", "-", -1)
}

func (d *DockerExecutor) Play(docID string) (int, error) {
	fetcher := d.MottainaiClient
	th := DefaultTaskHandler()
	task_info := th.FetchTask(fetcher)

	var sharedName, OriginalSharedName string
	image := task_info.Image

	u, err := url.Parse(task_info.Source)
	if err != nil {
		OriginalSharedName = image + task_info.Directory
	} else {
		OriginalSharedName = image + u.Path + task_info.Directory
	}

	sharedName, err = utils.StrictStrip(OriginalSharedName)
	if err != nil {
		panic(err)
	}

	artdir := d.Context.ArtefactDir
	storagetmp := d.Context.StorageDir
	git_repo_dir := d.Context.SourceDir

	var execute_script = "mottainai-run"

	if len(task_info.Script) > 0 {
		execute_script = strings.Join(task_info.Script, " && ")
	}
	// XXX: To replace with PID handling and background process.
	// XXX: Exp. in docker container
	// XXX: Start with docker, monitor it.
	// XXX: optional args, with --priviledged and -v socket
	docker_client := d.DockerClient

	if len(task_info.Image) > 0 {

		if len(task_info.CacheImage) > 0 {

			if img, err := d.FindImage(sharedName); err == nil {
				fetcher.AppendTaskOutput("Cached image found: " + img + " " + sharedName)
				if len(task_info.CacheClean) > 0 {
					fetcher.AppendTaskOutput("Not using previously cached image - deleting image: " + sharedName)
					d.RemoveImage(sharedName)
				} else {
					image = img
				}
			} else {
				fetcher.AppendTaskOutput("No cached image found locally for '" + sharedName + "'")
			}

			if len(task_info.CacheClean) == 0 {
				// Retrieve cached image into the hub
				if t, oktype := setting.Configuration.CacheRegistryCredentials["type"]; oktype && t == "docker" {

					toPull := purgeImageName(sharedName)
					if e, ok := setting.Configuration.CacheRegistryCredentials["entity"]; ok {
						toPull = e + "/" + toPull
					}
					fetcher.AppendTaskOutput("Try to pull cache (" + toPull + ") image from defined registry or from dockerhub")

					if baseUrl, okb := setting.Configuration.CacheRegistryCredentials["baseurl"]; okb {
						toPull = baseUrl + toPull
					}

					if e := d.PullImage(toPull); e == nil {
						image = toPull
						fetcher.AppendTaskOutput("Using pulled image:  " + image)
					} else {
						fetcher.AppendTaskOutput("No image could be fetched by cache registry")
					}
				}
			}

		}

		fetcher.AppendTaskOutput("Pulling image: " + task_info.Image)
		if err := d.PullImage(task_info.Image); err != nil {
			return 1, err
		}
		fetcher.AppendTaskOutput("Pulling image: DONE!")
	}
	//var args []string
	var git_root_path = d.Context.RootTaskDir
	var git_build_root_path = path.Join(git_root_path, task_info.Directory)

	var storage_path = "storage"
	var artefact_path = "artefacts"

	if len(task_info.ArtefactPath) > 0 {
		artefact_path = task_info.ArtefactPath
	}

	if len(task_info.StoragePath) > 0 {
		storage_path = task_info.StoragePath
	}

	var storage_root_path = path.Join(git_build_root_path, storage_path)

	var ContainerBinds []string

	var artefactdir string
	var storagedir string

	for _, b := range task_info.Binds {
		ContainerBinds = append(ContainerBinds, b)
	}

	if setting.Configuration.DockerInDocker {
		ContainerBinds = append(ContainerBinds, setting.Configuration.DockerEndpointDiD+":/var/run/docker.sock")
		ContainerBinds = append(ContainerBinds, "/tmp:/tmp")
		ContainerBinds = append(ContainerBinds, path.Join(git_build_root_path, artefact_path)+":"+path.Join(git_build_root_path, artefact_path))
		ContainerBinds = append(ContainerBinds, storage_root_path+":"+storage_root_path)

		artefactdir = path.Join(git_build_root_path, artefact_path)
		storagedir = storage_root_path
	} else {
		ContainerBinds = append(ContainerBinds, artdir+":"+path.Join(git_build_root_path, artefact_path))
		ContainerBinds = append(ContainerBinds, storagetmp+":"+storage_root_path)

		artefactdir = artdir
		storagedir = storagetmp
	}

	if err := d.DownloadArtefacts(artefactdir, storagedir); err != nil {
		return 1, err
	}

	//ContainerVolumes = append(ContainerVolumes, git_repo_dir+":/build")

	ContainerBinds = append(ContainerBinds, git_repo_dir+":"+git_root_path)

	createContHostConfig := docker.HostConfig{
		Privileged: setting.Configuration.DockerPriviledged,
		Binds:      ContainerBinds,
		CapAdd:     setting.Configuration.DockerCaps,
		CapDrop:    setting.Configuration.DockerCapsDrop,
		//	LogConfig:  docker.LogConfig{Type: "json-file"}
	}
	var containerconfig = &docker.Config{Image: image, WorkingDir: git_build_root_path}
	fetcher.AppendTaskOutput("Execute: " + execute_script)
	if len(execute_script) > 0 {
		containerconfig.Cmd = []string{"-c", "pwd;ls -liah;" + execute_script}
		containerconfig.Entrypoint = []string{"/bin/bash"}
	}

	if len(task_info.Entrypoint) > 0 {
		containerconfig.Entrypoint = task_info.Entrypoint
		fetcher.AppendTaskOutput("Entrypoint: " + strings.Join(containerconfig.Entrypoint, ","))
	}

	if len(task_info.Environment) > 0 {
		containerconfig.Env = task_info.Environment
		//	fetcher.AppendTaskOutput("Env: ")
		//	for _, e := range task_info.Environment {
		//		fetcher.AppendTaskOutput("- " + e)
		//	}
	}

	fetcher.AppendTaskOutput("Binds: ")
	for _, v := range ContainerBinds {
		fetcher.AppendTaskOutput("- " + v)
	}

	fetcher.AppendTaskOutput("Container working dir: " + git_build_root_path)
	fetcher.AppendTaskOutput("Image: " + containerconfig.Image)

	container, err := docker_client.CreateContainer(docker.CreateContainerOptions{
		Config:     containerconfig,
		HostConfig: &createContHostConfig,
	})

	if err != nil {
		panic(err)
	}

	utils.ContainerOutputAttach(func(s string) {
		fetcher.AppendTaskOutput(s)
	}, docker_client, container)
	defer d.CleanUpContainer(container.ID)
	if setting.Configuration.DockerKeepImg == false {
		defer d.RemoveImage(task_info.Image)
	}

	fetcher.AppendTaskOutput("Created container ID: " + container.ID)

	err = docker_client.StartContainer(container.ID, &createContHostConfig)
	if err != nil {
		panic(err)
	}
	fetcher.AppendTaskOutput("Started Container " + container.ID)

	starttime := time.Now()

	for {
		time.Sleep(1 * time.Second)
		now := time.Now()
		task_info = th.FetchTask(fetcher)
		timedout := (task_info.TimeOut != 0 && (now.Sub(starttime).Seconds() > task_info.TimeOut))
		if task_info.Status != "running" || timedout {
			if timedout {
				fetcher.AppendTaskOutput("Task timeout!")
			}
			fetcher.AppendTaskOutput("Aborting execution")
			docker_client.StopContainer(container.ID, uint(20))
			fetcher.AbortTask()
			return 1, errors.New("Task aborted")
		}
		c_data, err := docker_client.InspectContainer(container.ID) // update our container information
		if err != nil {
			//fetcher.SetTaskResult("error")
			//fetcher.SetTaskStatus("done")
			fetcher.AppendTaskOutput(err.Error())
			return 0, nil
		}
		if c_data.State.Running == false {

			var err error

			to_upload := artdir
			if setting.Configuration.DockerInDocker {
				to_upload = path.Join(git_root_path, task_info.Directory, artefact_path)
			}

			err = d.UploadArtefacts(to_upload)

			fetcher.AppendTaskOutput("Container execution terminated")

			if len(task_info.CacheImage) > 0 {
				fetcher.AppendTaskOutput("Saving container to " + sharedName)
				d.CommitImage(container.ID, sharedName, "latest")

				// Push image, if a cache_registry is configured in the node
				if err := d.PushImage(sharedName); err != nil {
					fetcher.AppendTaskOutput("Failed pushing image to cache registry: " + err.Error())
				} else {
					fetcher.AppendTaskOutput("Image pushed to cache registry successfully")
				}
			}

			if len(task_info.Prune) > 0 {
				fetcher.AppendTaskOutput("Pruning unused docker resources")
				d.Prune()
			}
			if err != nil {
				return 1, err
			}
			return c_data.State.ExitCode, nil
		}
	}

}

func (d *DockerExecutor) CommitImage(containerID, repo, tag string) (string, error) {
	image, err := d.DockerClient.CommitContainer(docker.CommitContainerOptions{Container: containerID, Repository: repo, Tag: tag})
	if err != nil {
		return "", err
	}
	return image.ID, nil
}

func (d *DockerExecutor) FindImage(image string) (string, error) {
	images, err := d.DockerClient.ListImages(docker.ListImagesOptions{Filter: image})
	if err != nil {
		return "", err
	}
	if len(images) > 0 {
		return images[0].ID, nil
	}
	return "", errors.New("Image not found")
}

func (d *DockerExecutor) RemoveImage(image string) error {
	return d.DockerClient.RemoveImage(image)
}

func (d *DockerExecutor) NewImageFrom(image, newimage, tag string) error {
	images, err := d.DockerClient.ListImages(docker.ListImagesOptions{Filter: image})
	var id string
	if len(images) > 0 {
		id = images[0].ID
	}

	err = d.DockerClient.TagImage(id, docker.TagImageOptions{Repo: newimage, Tag: tag, Force: true})
	if err != nil {
		return err
	}
	return nil
}

func (d *DockerExecutor) CleanUpContainer(ID string) error {
	return d.DockerClient.RemoveContainer(docker.RemoveContainerOptions{
		ID:    ID,
		Force: true,
	})
}

func (d *DockerExecutor) PullImage(image string) error {

	username, okname := setting.Configuration.CacheRegistryCredentials["username"]
	password, okpassword := setting.Configuration.CacheRegistryCredentials["password"]
	auth := docker.AuthConfiguration{}
	if okname && okpassword {
		auth.Password = password
		auth.Username = username
	}
	if serveraddress, ok := setting.Configuration.CacheRegistryCredentials["serveraddress"]; ok {
		auth.ServerAddress = serveraddress
	}
	d.MottainaiClient.AppendTaskOutput("Pulling image: " + image)
	if err := d.DockerClient.PullImage(docker.PullImageOptions{Repository: image}, auth); err != nil {
		return err
	}

	return nil
}

func (d *DockerExecutor) PushImage(image string) error {

	// Push image, if a cache_registry is configured in the node
	if t, oktype := setting.Configuration.CacheRegistryCredentials["type"]; oktype && t == "docker" {

		username, okname := setting.Configuration.CacheRegistryCredentials["username"]
		password, okpassword := setting.Configuration.CacheRegistryCredentials["password"]

		if okname && okpassword {
			baseurl, okbaseurl := setting.Configuration.CacheRegistryCredentials["baseurl"]
			entity, okentity := setting.Configuration.CacheRegistryCredentials["entity"]
			imageopts := docker.PushImageOptions{}
			auth := docker.AuthConfiguration{}
			auth.Password = password
			auth.Username = username
			serveraddress, okserveraddress := setting.Configuration.CacheRegistryCredentials["serveraddress"]
			if okserveraddress {
				auth.ServerAddress = serveraddress
			}
			if okentity {
				imageopts.Name = entity + "/" + purgeImageName(image)
				if err := d.NewImageFrom(image, imageopts.Name, "latest"); err != nil {
					return err
				}
				d.MottainaiClient.AppendTaskOutput("Tagged image: " + image + " ----> " + imageopts.Name)
			} else {
				imageopts.Name = purgeImageName(image)
			}
			d.MottainaiClient.AppendTaskOutput("Pushing image: " + imageopts.Name)

			if okbaseurl {
				imageopts.Registry = baseurl
			}
			return d.DockerClient.PushImage(imageopts, auth)
		}
	}
	return nil
}

func (d *DockerExecutor) FindImageInHub(image string) (bool, error) {
	res, err := d.DockerClient.SearchImages(image)
	if err != nil {
		return false, err
	}
	for _, r := range res {
		if r.Name == image {
			return true, nil
		}
	}

	return false, nil
}
