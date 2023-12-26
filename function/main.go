package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/go-git/go-git/v5"
)

const BUCKET_NAME_ENV string = "GBAAS_COREBUCKET_NAME"
const BUCKET_REGION_ENV string = "GBAAS_COREBUCKET_REGION"
const ENTITY_ENV string = "GBAAS_ENTITIES"

const ENTITY_SEPERATOR string = ";"
const ENTITY_TYPESEPERATOR string = ":"
const ENTITY_TYPE_USER string = "USER"
const ENTITY_TYPE_ORGA string = "ORGA"

const BUCKET_REPO_PREFIX string = "repos"
const BUCKET_WEBDATA_KEY string = "web/repos.json"
const GITHUB_API_URL string = "https://api.github.com"

const LAMBDA_TMP_DIR string = "/tmp"

// Limit maximum repositorys so that issues cannot cause a unpayable AWS bill
const GITHUB_MAX_REQUESTS = 5000

/**
 * Data that is keept from the Github REST response
 */
type repository struct {
	FullName string `json:"full_name"`
	Description string `json:"description"`
	HtmlUrl string `json:"html_url"`
	CloneUrl string `json:"clone_url"`
	Language string `json:"language"`
	Fork bool `json:"fork"`
}

type entity struct {
	name string
	typ string
}

/**
 * Fetches a list with all public repositories of a entity
 * @param name Name of the entity
 * @param orga Entity is organisation (if set to false entity is identified as user)
 */
func ListRepositories(name string, orga bool) ([]repository, error) {
	var baseUrl string
	if orga {
		baseUrl = fmt.Sprintf("%s/orgs/%s/repos", GITHUB_API_URL, name)
	} else {
		baseUrl= fmt.Sprintf("%s/users/%s/repos", GITHUB_API_URL, name)
	}
	
	httpClient := http.Client{
		Timeout: 30 * time.Second,
	}

	var repos []repository
	idx := 1

	// Iterate over pagination of github's api
	for i:=0; i<GITHUB_MAX_REQUESTS; i++ {
		reqUrl := fmt.Sprintf("%s?per_page=100&page=%d", baseUrl, idx)
		req, err := http.NewRequest(http.MethodGet, reqUrl, nil)
		if err!=nil {
			return nil, err
		}
		res, err := httpClient.Do(req)
		if err!=nil {
			 return nil, err
		}
		body, err := io.ReadAll(res.Body)
		if err!=nil {
			return nil, err
		}
		// Stop fetching (happens when e.g. a repo does not exist anymore)
		if res.StatusCode!=http.StatusOK {
			break
		}
		var tmpRepoList []repository
		err = json.Unmarshal(body, &tmpRepoList)
		if err!=nil {
			return nil, err
		}
		
		// Page is empty stop fetching of pages
		if len(tmpRepoList)==0 {
			break
		}
		repos = append(repos, tmpRepoList...)
		idx++
	}

	return repos, nil
}

/**
 * Puts the repositories in json format to the bucket
 * This data will be used from the stateless UI to show the current repositories
 * @param bucketname Name of the Corebucket
 * @param sess Session to the Corebucket
 * @param repos List of repositories
 */
func UpdateWebData(bucketname string, sess *s3.S3, repos []repository) error {
	data, err := json.Marshal(repos)
	_, err = sess.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(bucketname),
		Key: aws.String(BUCKET_WEBDATA_KEY),
		Body: bytes.NewReader(data),
	})
	if err!=nil {
		return err
	}
	return nil
}

/**
 * Fetches a repository bundle and returns it as bytestream
 * @param url Repository url
 */
func FetchRepository(url string) ([]byte, error) {
	bareRepoPath := fmt.Sprintf("%s/repobuf", LAMBDA_TMP_DIR)
	_, err := git.PlainClone(bareRepoPath, true, &git.CloneOptions{
		URL: url,
	})
	if err!=nil {
		return nil, err
	}

	bareRepoBuf := new(bytes.Buffer)

	zw := zip.NewWriter(bareRepoBuf)
	defer zw.Close()

	err = filepath.Walk(bareRepoPath, func(file string, fi os.FileInfo, err error) error {
		if err!=nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}

		header, err := zip.FileInfoHeader(fi)
		if err!=nil {
			return err
		}
		header.Name = file
		header.Method = zip.Deflate

		writer, err := zw.CreateHeader(header)
		if err!=nil {
			return err
		}

		curFile, err := os.Open(file)
		if err!=nil {
			return err
		}
		defer curFile.Close()
		_, err = io.Copy(writer, curFile)
		return err
	})
	if err!=nil {
		return nil, err
	}

	os.RemoveAll(bareRepoPath)

	return bareRepoBuf.Bytes(), nil
}

/**
 * Pushes the repository bundle to the Corebucket
 * @param name Name of the repository
 * @param bucketname Name of the Corebucket
 * @param sess Session to the Corebucket
 * @param data Repository bundle
 */
func PushRepository(name string, bucketname string, sess *s3.S3, data []byte) error {
	_, err := sess.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(bucketname),
		Key: aws.String(fmt.Sprintf("%s/%s.git.zip", BUCKET_REPO_PREFIX, name)),
		Body: bytes.NewReader(data),
	})
	if err!=nil {
		return err
	}
	return nil
}

/**
 * Starts a full backup job
 */
func StartJob(ctx context.Context) (string, error) {
	bucketName := os.Getenv(BUCKET_NAME_ENV)
	if bucketName=="" {
		return "Initialization Error", fmt.Errorf("Environment variable %s not specified\n", BUCKET_NAME_ENV)
	}
	
	region := os.Getenv(BUCKET_REGION_ENV)
	if region=="" {
		return "Initialization Error", fmt.Errorf("Environment variable %s not specified\n", BUCKET_REGION_ENV)
	}
	
	entityEnv := os.Getenv(ENTITY_ENV)
	if entityEnv=="" {
		return "Initialization Error", fmt.Errorf("Environment variable %s not specified\n", ENTITY_ENV)
	}
	var entities []entity
	for _, part := range strings.Split(entityEnv, ENTITY_SEPERATOR) {
		var entity entity
		blocks := strings.Split(part, ENTITY_TYPESEPERATOR)
		if len(blocks)!=2 {
			return "Initialization Error", fmt.Errorf("Failed to parse entities, expected NAME%sTYPE seperated by '%s'", ENTITY_TYPESEPERATOR, ENTITY_SEPERATOR)
		}
		entity.name = blocks[0]
		entity.typ = blocks[1]
		entities = append(entities, entity)
	}

	var repos []repository
	for _, entity := range entities {
		var tmpRepos []repository
		var err error
		if entity.typ==ENTITY_TYPE_ORGA {
			tmpRepos, err = ListRepositories(entity.name, true)
		} else if entity.typ==ENTITY_TYPE_USER {
			tmpRepos, err = ListRepositories(entity.name, false)
		}
		if err!=nil {
			return "Listing Error", err
		}
		repos = append(repos, tmpRepos...)
	}
	
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region),
	})
	if err!=nil {
		return "Connection Error", err
	}
	bucketSess:= s3.New(sess)

	// Repos are backed up one by one, if performance matters, this can be done async aswell.
	// Just be aware that this will lead to more memory usage -> more network bandwidth -> higher AWS bill -> AA (Anonymous AWS debtors) therapy sessions
	for _, repo := range repos {
		repoBytes, err := FetchRepository(repo.CloneUrl)
		if err!=nil {
			return "Fetching Error", err
		}
		err = PushRepository(repo.FullName, bucketName, bucketSess, repoBytes)
		if err!=nil {
			return "Pushing Error", err
		}
	}	

	err = UpdateWebData(bucketName, bucketSess, repos)
	if err!=nil {
		return "Pushing Error", err
	}
	return "", nil
}


func main() {
	lambda.Start(StartJob)
}
