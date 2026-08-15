package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type TemplateGenerator struct {
	config Config
}

func NewTemplateGenerator(config Config) TemplateGenerator {
	return TemplateGenerator{config: config}
}

func (generator TemplateGenerator) Write() error {
	template, err := os.ReadFile(generator.config.SourceTemplatePath)
	if err != nil {
		return err
	}

	output := string(template)
	output = strings.ReplaceAll(output,
		"https://raw.githubusercontent.com/aws-samples/personal-game-server-manager/main/Bash/valheim.sh",
		generator.s3ObjectURL("Bash/valheim.sh"),
	)
	output = generator.replaceFrontendURLs(output)
	output = generator.replaceLambdaCodeBlocks(output)
	output = generator.removeCopyLambdaDependency(output)
	output = generator.disableLambdaFileCopy(output)

	if err := os.MkdirAll(filepath.Dir(generator.config.OutputTemplatePath), 0755); err != nil {
		return err
	}
	return os.WriteFile(generator.config.OutputTemplatePath, []byte(output), 0644)
}

func (generator TemplateGenerator) replaceFrontendURLs(template string) string {
	files := []string{
		"styles.css",
		"index.html",
		"signed_in.html",
		"logout.html",
		"js/config.js",
		"js/index.js",
	}

	output := template
	for _, file := range files {
		output = strings.ReplaceAll(output,
			"https://raw.githubusercontent.com/aws-samples/personal-game-server-manager/main/FrontEnd/"+file,
			generator.s3ObjectURL("FrontEnd/"+file),
		)
	}
	return output
}

func (generator TemplateGenerator) replaceLambdaCodeBlocks(template string) string {
	output := template
	output = generator.replaceCodeBlock(output, "mcStartStopLambda:", "gaming_server_start_stop-v1_0.zip")
	output = generator.replaceCodeBlock(output, "mcUpdateDnsLambda:", "mcUpdateDNS-v1_0.zip")
	return output
}

func (generator TemplateGenerator) replaceCodeBlock(template, resourceMarker, zipName string) string {
	resourceIndex := strings.Index(template, "  "+resourceMarker)
	if resourceIndex == -1 {
		panic(fmt.Sprintf("resource marker %q not found", resourceMarker))
	}

	codeIndex := strings.Index(template[resourceIndex:], "      Code:\n")
	if codeIndex == -1 {
		panic(fmt.Sprintf("Code block for %q not found", resourceMarker))
	}
	codeIndex += resourceIndex

	blockStart := codeIndex + len("      Code:\n")
	blockEnd := codeBlockEnd(template, blockStart)
	replacement := fmt.Sprintf(
		"        S3Bucket: %s\n        S3Key: %s\n",
		generator.config.AssetBucketName,
		generator.s3Key("Lambda/"+zipName),
	)

	return template[:blockStart] + replacement + template[blockEnd:]
}

func codeBlockEnd(template string, blockStart int) int {
	lines := strings.SplitAfter(template[blockStart:], "\n")
	offset := blockStart
	for _, line := range lines {
		if strings.HasPrefix(line, "      Description:") {
			return offset
		}
		offset += len(line)
	}
	panic("end of Lambda Code block not found")
}

func (generator TemplateGenerator) removeCopyLambdaDependency(template string) string {
	return strings.ReplaceAll(template, "    DependsOn: mcCopyLambdaFiles\n", "")
}

func (generator TemplateGenerator) disableLambdaFileCopy(template string) string {
	startMarker := "  mcCopyLambdaFiles:\n"
	start := strings.Index(template, startMarker)
	if start == -1 {
		return template
	}

	endMarker := "\n  mcUpdateConfig:\n"
	relativeEnd := strings.Index(template[start:], endMarker)
	if relativeEnd == -1 {
		panic("mcCopyLambdaFiles block end not found")
	}
	end := start + relativeEnd

	return template[:start] + template[end+1:]
}

func (generator TemplateGenerator) s3ObjectURL(key string) string {
	return fmt.Sprintf(
		"https://%s.s3.%s.amazonaws.com/%s",
		generator.config.AssetBucketName,
		generator.config.AwsRegion,
		generator.s3Key(key),
	)
}

func (generator TemplateGenerator) s3Key(key string) string {
	return strings.Trim(generator.config.AssetKeyPrefix, "/") + "/" + strings.TrimLeft(key, "/")
}
