package main

import (
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/docker/engine-api/client"
	"github.com/docker/engine-api/types"
	"golang.org/x/net/context"
)

type elbTarget struct {
	instanceID   string
	instancePort int
}
type elbTargetSlice []elbTarget

func (targets elbTargetSlice) contain(target elbTarget) bool {
	for _, t := range targets {
		if target == t {
			return true
		}
	}
	return false
}

func getContainerVersion(name string) (version int) {
	re := regexp.MustCompile("v(\\d+)")
	matches := re.FindStringSubmatch(name)
	if len(matches) == 2 {
		version, _ = strconv.Atoi(matches[1])
	}
	return
}

func getNewTargets(instanceID string, dockerPrefix string) elbTargetSlice {
	defaultHeaders := map[string]string{"User-Agent": "elb-docker-sync"}
	cli, err := client.NewClient("unix:///var/run/docker.sock", "v1.22", nil, defaultHeaders)
	if err != nil {
		panic(err)
	}

	options := types.ContainerListOptions{All: true}
	containers, err := cli.ContainerList(context.Background(), options)
	if err != nil {
		panic(err)
	}

	// We only keep containers that have this version (to enable rolling deploys)
	highestVersion := 0
	matchedContainers := make(map[int][]types.Container)

	for _, c := range containers {
		name := c.Names[0][1:]
		if !strings.HasPrefix(name, dockerPrefix) {
			continue
		}

		version := getContainerVersion(name)
		matchedContainers[version] = append(matchedContainers[version], c)

		if version > highestVersion {
			highestVersion = version
		}
	}

	targets := elbTargetSlice{}
	for _, c := range matchedContainers[highestVersion] {
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}

			targets = append(targets, elbTarget{instanceID: instanceID, instancePort: p.PublicPort})
		}
	}

	return targets
}

func getTargetGroupArn(elbSvc *elbv2.ELBV2, targetGroupName string) string {
	params := &elbv2.DescribeTargetGroupsInput{
		Names:    []*string{aws.String(targetGroupName)},
		PageSize: aws.Int64(1),
	}
	resp, err := elbSvc.DescribeTargetGroups(params)
	if err != nil {
		panic(err.Error())
	}

	return *resp.TargetGroups[0].TargetGroupArn
}

func getOldTargets(elbSvc *elbv2.ELBV2, instanceID string, targetGroupArn string) elbTargetSlice {
	hParams := &elbv2.DescribeTargetHealthInput{TargetGroupArn: aws.String(targetGroupArn)}
	hResp, err := elbSvc.DescribeTargetHealth(hParams)
	if err != nil {
		panic(err.Error())
	}

	targets := elbTargetSlice{}

	for _, target := range hResp.TargetHealthDescriptions {
		if *target.Target.Id == instanceID {
			targets = append(targets, elbTarget{instanceID: *target.Target.Id, instancePort: int(*target.Target.Port)})
		}
	}

	return targets
}

func registerTarget(elbSvc *elbv2.ELBV2, targetGroupArn string, target elbTarget) {
	params := &elbv2.RegisterTargetsInput{
		TargetGroupArn: aws.String(targetGroupArn),
		Targets: []*elbv2.TargetDescription{
			{Id: aws.String(target.instanceID), Port: aws.Int64(int64(target.instancePort))},
		},
	}
	_, err := elbSvc.RegisterTargets(params)

	if err != nil {
		panic(err.Error())
	}
}

func deregisterTarget(elbSvc *elbv2.ELBV2, targetGroupArn string, target elbTarget) {
	params := &elbv2.DeregisterTargetsInput{
		TargetGroupArn: aws.String(targetGroupArn),
		Targets: []*elbv2.TargetDescription{
			{Id: aws.String(target.instanceID), Port: aws.Int64(int64(target.instancePort))},
		},
	}
	_, err := elbSvc.DeregisterTargets(params)

	if err != nil {
		panic(err.Error())
	}
}

func getInstanceID(sess *session.Session) string {
	svc := ec2metadata.New(sess)
	instanceIdentity, err := svc.GetInstanceIdentityDocument()
	if err != nil {
		panic(err)
	}
	return instanceIdentity.InstanceID
}

func processAll(logger *log.Logger, elbSvc *elbv2.ELBV2, instanceID string) {
	//logger.Print("Syncing...")
	for _, arg := range os.Args[1:] {
		pieces := strings.Split(arg, ",")
		dockerPrefix := pieces[0]
		targetGroupName := pieces[1]
		targetGroupArn := getTargetGroupArn(elbSvc, targetGroupName)
		oldTargets := getOldTargets(elbSvc, instanceID, targetGroupArn)
		newTargets := getNewTargets(instanceID, dockerPrefix)

		//logger.Printf("[%s] Found %d old targets, %d new targets", targetGroupName, len(oldTargets), len(newTargets))

		// Don't take any action on encountering an empty list - we might be getting bad data
		if len(newTargets) == 0 {
			//logger.Printf("[%s] Skipping since no containers are matched", targetGroupName)
			continue
		}

		for _, target := range newTargets {
			if !oldTargets.contain(target) {
				logger.Printf("[%s] Registering %+v", targetGroupName, target)
				registerTarget(elbSvc, targetGroupArn, target)
			}
		}

		for _, target := range oldTargets {
			if !newTargets.contain(target) {
				logger.Printf("[%s] De-Registering %+v", targetGroupName, target)
				deregisterTarget(elbSvc, targetGroupArn, target)
			}
		}
	}
}

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)
	creds := credentials.NewCredentials(&ec2rolecreds.EC2RoleProvider{Client: ec2metadata.New(session.New())})
	sess := session.New(&aws.Config{Credentials: creds, Region: aws.String("us-east-1")})
	elbSvc := elbv2.New(sess)
	instanceID := getInstanceID(sess)

	processAll(logger, elbSvc, instanceID)

	ticker := time.NewTicker(time.Second * 5).C
	for {
		select {
		case <-ticker:
			processAll(logger, elbSvc, instanceID)
		}
	}
}
