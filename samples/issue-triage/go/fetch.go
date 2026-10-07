// Reading issues from GitHub.
//
// One GraphQL query per page gets everything both stages need: the text for
// stage one, and the facts for stage two. The facts are the reason this is
// GraphQL rather than REST. Whether a pull request references an issue lives in
// the issue's timeline, and the REST issues endpoint does not carry it, so a
// REST client would need a second call per issue to find out.
//
// Needs a GitHub token and no Jev key.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// graphQLURL is GitHub's GraphQL endpoint.
	graphQLURL = "https://api.github.com/graphql"
	// issuesPerPage is how many issues one query reads. A page of 25 costs a
	// handful of rate-limit points of an hourly 5,000.
	issuesPerPage = 25
	// timelineItems is how many timeline entries to read per issue. The only
	// thing wanted from the timeline is whether a pull request references the
	// issue, and no issue in the reference repository carries more than a few.
	timelineItems = 20
	// commentsPerIssue is how many comments to read per issue. The counts derived
	// from them say who has been talking, and an issue past this reports its true
	// total alongside the fact that the breakdown is partial.
	commentsPerIssue = 100
	// maxPages stops a walk of a repository with 25,000 open issues.
	maxPages = 40
	// githubTimeout caps one query.
	githubTimeout = 60 * time.Second
	// defaultLimit is how many issues to read when the caller names no selector.
	defaultLimit = 0
)

// tokenEnvNames are where the environment carries a GitHub token, in order.
var tokenEnvNames = []string{"GITHUB_TOKEN", "GH_TOKEN"}

// issueQuery reads one page of issues in a given state.
//
// The cursor variable is named endCursor to match GitHub's own paging field.
// This pages by hand rather than through `gh api --paginate`, which substitutes
// that one name only and loops forever on any other.
const issueQuery = `
query($owner: String!, $name: String!, $perPage: Int!, $timeline: Int!,
      $comments: Int!, $states: [IssueState!], $endCursor: String) {
  rateLimit { remaining }
  repository(owner: $owner, name: $name) {
    issues(first: $perPage, after: $endCursor, states: $states,
           orderBy: {field: UPDATED_AT, direction: DESC}) {
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        title
        body
        state
        createdAt
        updatedAt
        closedAt
        authorAssociation
        author { login __typename }
        comments(first: $comments) {
          totalCount
          nodes {
            authorAssociation
            author { login __typename }
          }
        }
        labels(first: 20) { nodes { name } }
        milestone { title }
        timelineItems(first: $timeline, itemTypes: [CROSS_REFERENCED_EVENT]) {
          nodes {
            ... on CrossReferencedEvent {
              source { ... on PullRequest { number state } }
            }
          }
        }
      }
    }
  }
}
`

// wireActor is whoever wrote something: a person, a bot, or nobody when the
// account has since been deleted.
//
// TypeName is GitHub's own answer to "is this a bot", which beats guessing from
// the name. The name still gets checked, because an app posting through a user
// account comes back as a User with a [bot] suffix.
type wireActor struct {
	Login    string `json:"login"`
	TypeName string `json:"__typename"`
}

// IsBot says whether this actor is automation.
func (a *wireActor) IsBot() bool {
	if a == nil {
		return false
	}
	return a.TypeName == "Bot" || strings.HasSuffix(a.Login, "[bot]")
}

// Name returns the login, or a placeholder for a deleted account.
func (a *wireActor) Name() string {
	if a == nil || a.Login == "" {
		return "(deleted)"
	}
	return a.Login
}

// wireIssue is one issue as the query returns it.
type wireIssue struct {
	Number            int        `json:"number"`
	Title             string     `json:"title"`
	Body              string     `json:"body"`
	State             string     `json:"state"`
	CreatedAt         string     `json:"createdAt"`
	UpdatedAt         string     `json:"updatedAt"`
	ClosedAt          string     `json:"closedAt"`
	AuthorAssociation string     `json:"authorAssociation"`
	Author            *wireActor `json:"author"`
	Comments          struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			AuthorAssociation string     `json:"authorAssociation"`
			Author            *wireActor `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	TimelineItems struct {
		Nodes []struct {
			Source *struct {
				Number int    `json:"number"`
				State  string `json:"state"`
			} `json:"source"`
		} `json:"nodes"`
	} `json:"timelineItems"`
}

// graphQLResponse is the body one query returns.
type graphQLResponse struct {
	Data *struct {
		RateLimit struct {
			Remaining int `json:"remaining"`
		} `json:"rateLimit"`
		Repository *struct {
			Issues struct {
				TotalCount int `json:"totalCount"`
				PageInfo   struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []wireIssue `json:"nodes"`
			} `json:"issues"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// githubToken finds a GitHub token, preferring the environment over the gh CLI.
//
// The GraphQL API refuses anonymous callers outright, so this fails rather than
// falling back to an unauthenticated request the way a REST client can.
func githubToken() (string, string, error) {
	for _, name := range tokenEnvNames {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value, "the " + name + " environment variable", nil
		}
	}

	// #nosec G204 - a fixed command with no user input
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		if token := strings.TrimSpace(string(out)); token != "" {
			return token, "the gh CLI", nil
		}
	}
	return "", "", fmt.Errorf(
		"GitHub's GraphQL API needs a token. Set GITHUB_TOKEN, or run `gh auth login`",
	)
}

// postGraphQL runs one page of the query.
//
// A GraphQL error arrives with HTTP 200 and a null data, so a client that only
// checks the status code reads a failed query as an empty page. This checks both.
func postGraphQL(token string, variables map[string]any) (*graphQLResponse, error) {
	body, err := json.Marshal(map[string]any{"query": issueQuery, "variables": variables})
	if err != nil {
		return nil, fmt.Errorf("encoding the query: %w", err)
	}

	request, err := http.NewRequest(http.MethodPost, graphQLURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building the query request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "jev-samples-issue-triage")

	client := &http.Client{Timeout: githubTimeout}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("calling GitHub: %w", err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading GitHub's answer: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf(
			"GitHub returned %d: the token is missing, expired, or lacks access",
			response.StatusCode,
		)
	}

	var parsed graphQLResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parsing GitHub's answer: %w", err)
	}
	if len(parsed.Errors) > 0 {
		messages := make([]string, 0, len(parsed.Errors))
		for _, e := range parsed.Errors {
			messages = append(messages, e.Message)
		}
		return nil, fmt.Errorf("GitHub GraphQL reported an error: %s", strings.Join(messages, "; "))
	}
	if parsed.Data == nil || parsed.Data.Repository == nil {
		return nil, fmt.Errorf(
			"GitHub returned no repository for %v/%v", variables["owner"], variables["name"],
		)
	}
	return &parsed, nil
}

// fetchIssues reads a repository's issues in one state.
//
// limit of 0 means every one, up to maxPages.
func fetchIssues(repo string, state string, limit int) ([]issue, int, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, 0, fmt.Errorf("not a repository: %s, pass owner/repo", repo)
	}

	token, source, err := githubToken()
	if err != nil {
		return nil, 0, err
	}
	fmt.Fprintf(os.Stderr, "issue-triage: reading %s %s issues, token from %s\n",
		repo, strings.ToLower(state), source)

	var out []issue
	cursor := ""
	total := 0

	for page := 1; page <= maxPages; page++ {
		variables := map[string]any{
			"owner": owner, "name": name,
			"perPage": issuesPerPage, "timeline": timelineItems,
			"comments": commentsPerIssue,
			"states":   []string{state},
		}
		// A null cursor asks for the first page. An empty string is an error to
		// GitHub rather than a first page.
		if cursor == "" {
			variables["endCursor"] = nil
		} else {
			variables["endCursor"] = cursor
		}

		parsed, err := postGraphQL(token, variables)
		if err != nil {
			return out, total, err
		}
		issues := parsed.Data.Repository.Issues
		total = issues.TotalCount

		for _, node := range issues.Nodes {
			out = append(out, convert(node))
		}
		if limit > 0 && len(out) >= limit {
			return out[:limit], total, nil
		}
		if !issues.PageInfo.HasNextPage {
			return out, total, nil
		}
		cursor = issues.PageInfo.EndCursor
	}

	fmt.Fprintf(os.Stderr, "issue-triage: stopped at %d pages\n", maxPages)
	return out, total, nil
}

// maintainerRoles are the authorAssociation values that mean somebody speaks
// for the project.
var maintainerRoles = map[string]bool{
	"OWNER": true, "MEMBER": true, "COLLABORATOR": true,
}

// countComments works out who has been talking on an issue.
//
// Three counts and a headcount, because they say different things. The reporter
// posting four times with no reply is an issue waiting on us. A maintainer
// having replied means somebody has already looked. Several different people
// turning up means more than one person is affected, which a single reporter's
// insistence cannot tell you.
//
// Bots are excluded from all of it. A CI bot posting twelve build failures would
// otherwise read as a busy, contested thread.
func countComments(node wireIssue) discussion {
	out := discussion{
		Total:                node.Comments.TotalCount,
		ReporterIsMaintainer: maintainerRoles[node.AuthorAssociation],
	}
	author := node.Author.Name()
	people := map[string]bool{}

	for _, c := range node.Comments.Nodes {
		if c.Author.IsBot() {
			out.Bots++
			continue
		}
		name := c.Author.Name()
		// A deleted account cannot be told apart from another deleted account, so
		// it counts toward the comment totals and not toward the headcount.
		if name != "(deleted)" {
			people[name] = true
		}

		// The two roles are counted independently rather than as a switch,
		// because one comment can be both. A maintainer who filed the issue and
		// then replied on it is doing both things at once.
		byAuthor := name == author && author != "(deleted)"
		byMaintainer := maintainerRoles[c.AuthorAssociation]
		if byAuthor {
			out.ByReporter++
		}
		if byMaintainer {
			out.ByMaintainer++
		}
		if !byAuthor && !byMaintainer {
			out.ByOthers++
		}
	}

	out.People = len(people)
	// GitHub returns the true total and at most one page of comments, so a long
	// thread has a breakdown covering only part of it. Saying so beats quietly
	// reporting the part as the whole.
	out.Partial = out.Total > len(node.Comments.Nodes)
	return out
}

// convert turns a query node into the issue the scorer reads.
//
// The linked pull request is the one fact worth explaining. A cross-reference is
// any pull request that mentioned the issue, and an open one is the signal that
// somebody is working on this now. An open one wins over a merged one, because
// a merged pull request that did not close the issue left something behind.
func convert(node wireIssue) issue {
	labels := make([]string, 0, len(node.Labels.Nodes))
	for _, l := range node.Labels.Nodes {
		labels = append(labels, l.Name)
	}

	linked, open := 0, false
	for _, item := range node.TimelineItems.Nodes {
		if item.Source == nil || item.Source.Number == 0 {
			continue
		}
		if item.Source.State == "OPEN" {
			linked, open = item.Source.Number, true
			break
		}
		if linked == 0 {
			linked = item.Source.Number
		}
	}

	milestone := ""
	if node.Milestone != nil {
		milestone = node.Milestone.Title
	}

	talk := countComments(node)

	return issue{
		Discussion:        talk,
		Number:            node.Number,
		Title:             node.Title,
		Body:              node.Body,
		Labels:            labels,
		Milestone:         milestone,
		AuthorAssociation: node.AuthorAssociation,
		LinkedPullRequest: linked,
		LinkedPullOpen:    open,
		CreatedAt:         node.CreatedAt,
		UpdatedAt:         node.UpdatedAt,
		State:             node.State,
		ClosedAt:          node.ClosedAt,
	}
}
