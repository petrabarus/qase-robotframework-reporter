package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/beevik/etree"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	qase "go.qase.io/client"
)

type Config struct {
	Filename     string
	QaseApiToken string `mapstructure:"api_token"`
	QaseProject  string `mapstructure:"project"`
	QaseRunTitle string `mapstructure:"run_title"`
}

type TestCaseResult struct {
	Package    string
	TestCaseId int64
	Status     string
	Time       time.Time
	TimeMs     int64
}

const (
	TEST_RESULT_STATUS_PASSED = "passed"
	TEST_RESULT_STATUS_FAILED = "failed"

	//
	V6_TIME_PATTERN = "20060102 15:04:05.999"
	V7_TIME_PATTERN = "2006-01-02T15:04:05.999999999"
)

var (
	ctx context.Context

	config Config

	cmd = &cobra.Command{
		Use:   "qase-robotframework-reporter <filename>",
		Short: "qase-robotframework-reporter is a tool to report Robot Framework test results to Qase",
		Long: `qase-robotframework-reporter is a tool to report Robot Framework test results to Qase.
This will read the Robot Framework output.xml file and report the results to Qase. 
This is an alternative to the Robot Framework Qase library, which is not suitable for my use case.
`,
		Args:             cobra.ExactArgs(1),
		ArgAliases:       []string{"filename"},
		PersistentPreRun: preRun,
		Run:              RunCommand,
	}

	qaseClient  qase.APIClient   // Qase API client
	testResults []TestCaseResult // Stores the case result from XML and pass to Qase
	xmlDoc      *etree.Document  // Stores the XML document
)

func init() {
	cobra.OnInitialize()

	cmd.Flags().StringP("project", "p", "", "Qase project name")
	cmd.Flags().StringP("api_token", "t", "", "Qase API token")
	cmd.Flags().StringP("run_title", "r", "", "Qase run title")

	viper.BindPFlag("project", cmd.Flags().Lookup("project"))
	viper.BindPFlag("api_token", cmd.Flags().Lookup("api-token"))
	viper.BindPFlag("run_title", cmd.Flags().Lookup("run-title"))

	// Adopts the official Qase environment variables
	viper.BindEnv("project", "QASE_TESTOPS_PROJECT")
	viper.BindEnv("api_token", "QASE_TESTOPS_API_TOKEN")
	viper.BindEnv("run_title", "QASE_TESTOPS_RUN_TITLE")
}

func main() {
	err := cmd.Execute()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func preRun(cmd *cobra.Command, args []string) {
	viper.AutomaticEnv()
	err := viper.Unmarshal(&config)
	if err != nil {
		log.Fatalf("Unable to read Viper options into configuration: %v", err)
	}
	config.Filename = args[0]

	//log.Printf("Config: %+v", config)
	ctx = context.Background()

	initQaseClient()
}

func initQaseClient() {
	configuration := qase.NewConfiguration()
	configuration.AddDefaultHeader("Token", config.QaseApiToken)
	qaseClient = *qase.NewAPIClient(configuration)
}

func RunCommand(cmd *cobra.Command, args []string) {
	var err error
	fmt.Println("Running qase-robotframework-reporter")
	if err = readXmlFile(); err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	// Parse XML
	if err = parseTestResultsFromXml(); err != nil {
		log.Fatalf("Error parsing XML: %v", err)
	}

	// Report to Qase
	if err = reportToQase(); err != nil {
		log.Fatalf("Error reporting to Qase: %v", err)
	}
}

func readXmlFile() (err error) {
	// Print absolute path
	if filename, err := filepath.Abs(config.Filename); err == nil {
		log.Println("Reading file: ", filename)
	}

	// Openfile
	xmlDoc = etree.NewDocument()
	if err = xmlDoc.ReadFromFile(config.Filename); err != nil {
		err = fmt.Errorf("error reading XML file: %v", err)
		return
	}

	return
}

func parseTestResultsFromXml() (err error) {
	root := xmlDoc.Root()
	if root == nil || root.Tag != "robot" {
		err = fmt.Errorf("cannot find robot root node")
		return
	}

	testResults = make([]TestCaseResult, 0)
	for _, childElmt := range root.FindElements("//test") {
		//fmt.Println(childElmt.Tag)

		testResult, pErr := parseTestResultFromTestXmlElement(childElmt)
		if pErr != nil {
			log.Printf("Error parsing test result: %v", pErr)
			continue
		}
		testResults = append(testResults, testResult)
	}

	return
}

func reportToQase() (err error) {
	runId, err := createNewQaseRun()
	if err != nil {
		log.Fatalf("Failed to create test run: %v", err)
	}

	err = createQaseTestRunResults(runId)
	if err != nil {
		log.Fatalf("Failed to create test run result: %v", err)
	}

	err = completeQaseRun(runId)
	if err != nil {
		log.Fatalf("Failed to complete test run: %v", err)
	}
	return
}

func parseTestResultFromTestXmlElement(element *etree.Element) (result TestCaseResult, err error) {
	// assume we have 1 tag for now

	result.TestCaseId, err = parseQaseIdFromTestElement(element)
	if err != nil {
		err = fmt.Errorf("error parsing Qase ID: %v", err)
		return
	}

	result.Status,
		result.Time,
		result.TimeMs,
		err = parseStatusAndTimeFromTestElement(element)
	log.Printf("Test case ID: %d, Status: %s, Time: %v, TimeMs: %d", result.TestCaseId, result.Status, result.Time, result.TimeMs)

	if err != nil {
		err = fmt.Errorf("error parsing status and time: %v", err)
		return
	}

	return
}

func parseQaseIdFromTestElement(element *etree.Element) (qaseId int64, err error) {
	// Get the ID from the tag in the children elements of the test element
	tags := element.SelectElements("tag")
	regex := regexp.MustCompile(`Q-(\d+)`)
	if len(tags) == 0 {
		err = fmt.Errorf("cannot find tag element")
		return
	}

	for _, tag := range tags {
		text := tag.Text()
		// check if format regex pattern is `Q-\d+`
		// if yes, assign the ID to qaseID
		if regex.MatchString(text) {
			qaseIdText := regex.FindStringSubmatch(text)[1]
			qaseId, err = strconv.ParseInt(qaseIdText, 10, 64)
			return
		}
	}

	if qaseId == 0 {
		err = fmt.Errorf("cannot find Qase ID in tags")
	}
	return
}

func parseStatusAndTimeFromTestElement(element *etree.Element) (status string, startTime time.Time, timeMs int64, err error) {
	statusTag := element.FindElement("status")
	if statusTag == nil {
		err = fmt.Errorf("cannot find status tag")
		return
	}

	status, err = parseStatusFromTestStatusElement(statusTag)
	if err != nil {
		err = fmt.Errorf("error parsing status: %v", err)
		return
	}
	robotXmlVersion := 7
	startTime, robotXmlVersion, err = parseStartTimeFromTestStatusElement(statusTag)
	if err != nil {
		err = fmt.Errorf("error parsing start time: %v", err)
		return
	}

	timeMs, err = parseTimeFromTestStatusElement(statusTag, startTime, robotXmlVersion)
	if err != nil {
		err = fmt.Errorf("error parsing time: %v", err)
		return
	}

	return
}

func parseStatusFromTestStatusElement(element *etree.Element) (status string, err error) {
	statusText := element.SelectAttrValue("status", "")
	if statusText == "" {
		err = fmt.Errorf("cannot find status attribute")
		return
	}
	if statusText == "PASS" {
		status = TEST_RESULT_STATUS_PASSED
	} else {
		status = TEST_RESULT_STATUS_FAILED
	}

	return
}

func parseStartTimeFromTestStatusElement(element *etree.Element) (startTime time.Time, robotXmlVersion int, err error) {
	robotXmlVersion = 7
	startTimeText := element.SelectAttrValue("start", "")
	if startTimeText == "" {
		// if no tag `start` then means it's robotframework version < 7.
		// the time tag used are `starttime` and `endtime` instead of
		// `start` and `elapsed`
		robotXmlVersion = 6
		startTimeText = element.SelectAttrValue("starttime", "")
		if startTimeText == "" {
			err = fmt.Errorf("cannot find starttime attribute")
			return
		}
		startTime, err = time.Parse(V6_TIME_PATTERN, startTimeText)
		if err != nil {
			err = fmt.Errorf("error parsing start time: %v", err)
			return
		}
	} else {
		startTime, err = time.Parse(V7_TIME_PATTERN, startTimeText)
		if err != nil {
			err = fmt.Errorf("error parsing start time: %v", err)
			return
		}
	}

	return
}

func parseTimeFromTestStatusElement(element *etree.Element, startTime time.Time, robotXmlVersion int) (timeMs int64, err error) {
	if robotXmlVersion == 7 {
		elapsedText := element.SelectAttrValue("elapsed", "")
		if elapsedText == "" {
			err = fmt.Errorf("cannot find elapsed attribute")
			return
		}
		var timeSeconds float64
		timeSeconds, err = strconv.ParseFloat(elapsedText, 64)
		if err != nil {
			err = fmt.Errorf("error parsing elapsed time: %v", err)
			return
		}
		timeMs = int64(timeSeconds * 1000)
		return
	}

	// use `endtime` attribute instead of `elapsed`
	endTimeText := element.SelectAttrValue("endtime", "")
	if endTimeText == "" {
		err = fmt.Errorf("cannot find endtime attribute")
		return
	}
	var endTime time.Time
	endTime, err = time.Parse(V6_TIME_PATTERN, endTimeText)
	if err != nil {
		err = fmt.Errorf("error parsing end time: %v", err)
		return
	}
	timeMs = int64(endTime.Sub(startTime).Milliseconds())
	return
}

func createNewQaseRun() (runId int32, err error) {
	// Create Test Run
	log.Printf("Creating test run")
	caseIds := make([]int64, 0)
	for _, result := range testResults {
		caseIds = append(caseIds, result.TestCaseId)
	}

	qaseResp, httpResp, err := qaseClient.RunsApi.CreateRun(ctx, qase.RunCreate{
		Title: config.QaseRunTitle,
		Cases: caseIds,
	}, config.QaseProject)
	if err != nil {
		err = fmt.Errorf("failed to create test run: %v", err)
		return
	}

	if httpResp.StatusCode != 200 {
		err = fmt.Errorf("failed to create test run, status code: %v", httpResp.StatusCode)
		return
	}

	runId = int32(qaseResp.Result.Id)
	log.Printf("Created test run ID: %d", runId)
	return
}

func createQaseTestRunResults(runId int32) (err error) {
	log.Printf("Creating test run results for run ID: %d", runId)
	qaseResults := make([]qase.ResultCreate, 0)
	for _, result := range testResults {
		qaseResult := qase.ResultCreate{
			CaseId: int64(result.TestCaseId),
			Status: result.Status,
			// Somewhat this result in bad request
			//Time:   result.Time.Unix(),
			TimeMs: result.TimeMs,
		}
		if result.Package != "" {
			qaseResult.Comment = fmt.Sprintf("Package: %v", result.Package)
		}
		qaseResults = append(qaseResults, qaseResult)
	}

	qaseResp, httpResp, err := qaseClient.ResultsApi.CreateResultBulk(ctx, qase.ResultCreateBulk{
		Results: qaseResults,
	}, config.QaseProject, runId)

	if err != nil {
		// read body to string
		message, _ := io.ReadAll(httpResp.Body)
		err = fmt.Errorf("failed to create test run results: %v %s", err, message)
		return
	}

	if httpResp.StatusCode != 200 {
		message, _ := io.ReadAll(httpResp.Body)
		err = fmt.Errorf("failed to create test run results, status code: %v %s", httpResp.StatusCode, message)
		return
	}

	if !qaseResp.Status {
		err = fmt.Errorf("failed to create test run results, status false")
		return
	}

	return
}

func completeQaseRun(runId int32) (err error) {
	// Complete Test Run
	log.Printf("Completing test run ID: %d", runId)
	qaseResp, httpResp, err := qaseClient.RunsApi.CompleteRun(
		ctx,
		config.QaseProject,
		runId,
	)
	if err != nil {
		err = fmt.Errorf("failed to complete test run: %v", err)
		return
	}

	if httpResp.StatusCode != 200 {
		err = fmt.Errorf("failed to complete test run, status code: %v", httpResp.StatusCode)
		return
	}

	if !qaseResp.Status {
		err = fmt.Errorf("failed to complete test run, status false")
		return
	}
	log.Printf("Completed test run ID: %d", runId)
	return nil
}
