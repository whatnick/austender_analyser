# Austender Summarizer
There is always news around how much the government spends on contracting. However it is hard
to find a quick summary of figures in a transparent manner without digging through Austender
website, so public service report or relying on journalists' scraping skills.

Set this up as a serverless go development project to answer spend questions by organization name e.g. KPMG had 5136 tenders awarded, what was the total value ?

![KPMG Tenders](docs/KPMG_result_2023_01_22.png)

## Features
- CLI tool that scrapes for a keyword and returns roll-ups
- Standard 3-tier design (BE,FE,DB)
- Backend implementation in Golang
- Frontend implementation in Angular/React TBD
- Fuzzy name matching in Google "did you mean" style
- Temporal aggregate spend on raw AUD values, not inflation adjusted (I am not an economist)
- Serverless hosting in AWS
- DynamoDB cache of Austender data downloadable as CSV


## Roadmap
- Download one search result - KPMG
- Identify fields to cache from Austender
- Cron to download and populate dynamodb with Austender info
- Webserver API to serve 1 set results
    - total spend on org all time
    - last 5 years
    - last year
- TDD
- Go project layout as it matures : https://github.com/golang-standards/project-layout