package handlers

import (
	"fmt"
	"github.com/go-openapi/runtime/middleware"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
	"github.com/lib/pq"
	"github.com/markbates/goth"
	"gitlab.com/comentario/comentario/internal/api/models"
	"gitlab.com/comentario/comentario/internal/api/restapi/operations"
	"gitlab.com/comentario/comentario/internal/svc"
	"gitlab.com/comentario/comentario/internal/util"
	"time"
)

const commentsRowColumns = `
	comments.commentHex,
	comments.commenterHex,
	comments.markdown,
	comments.html,
	comments.parentHex,
	comments.score,
	comments.state,
	comments.deleted,
	comments.creationDate
`

func CommentApprove(params operations.CommentApproveParams) middleware.Responder {
	c, err := commenterGetByCommenterToken(*params.Body.CommenterToken)
	if err != nil {
		return operations.NewCommentApproveOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	domain, _, err := commentDomainPathGet(*params.Body.CommentHex)
	if err != nil {
		return operations.NewCommentApproveOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	if isModerator, err := isDomainModerator(domain, c.Email); err != nil {
		return operations.NewCommentApproveOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	} else if !isModerator {
		return operations.NewCommentApproveOK().WithPayload(&models.APIResponseBase{Message: util.ErrorNotModerator.Error()})
	}

	if err = commentApprove(*params.Body.CommentHex); err != nil {
		return operations.NewCommentApproveOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	// Succeeded
	return operations.NewCommentApproveOK().WithPayload(&models.APIResponseBase{Success: true})
}

func CommentCount(params operations.CommentCountParams) middleware.Responder {
	commentCounts, err := commentCount(*params.Body.Domain, params.Body.Paths)
	if err != nil {
		return operations.NewCommentCountOK().WithPayload(&operations.CommentCountOKBody{Message: err.Error()})
	}

	// Succeeded
	return operations.NewCommentCountOK().WithPayload(&operations.CommentCountOKBody{
		Success:       true,
		CommentCounts: commentCounts,
	})
}

func CommentDelete(params operations.CommentDeleteParams) middleware.Responder {
	commenter, err := commenterGetByCommenterToken(*params.Body.CommenterToken)
	if err != nil {
		return operations.NewCommentDeleteOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	comment, err := commentGetByCommentHex(*params.Body.CommentHex)
	if err != nil {
		return operations.NewCommentDeleteOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	// If not deleting their own comment, the user must be a domain moderator
	if comment.CommenterHex != commenter.CommenterHex {
		// Fetch comment's domain
		if domain, _, err := commentDomainPathGet(*params.Body.CommentHex); err != nil {
			return operations.NewCommentDeleteOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
			// Find the domain moderator
		} else if isModerator, err := isDomainModerator(domain, commenter.Email); err != nil {
			return operations.NewCommentDeleteOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
			// Check the commenter is a domain moderator
		} else if !isModerator {
			return operations.NewCommentDeleteOK().WithPayload(&models.APIResponseBase{Message: util.ErrorNotModerator.Error()})
		}
	}

	if err = commentDelete(*params.Body.CommentHex, models.HexID(commenter.CommenterHex)); err != nil {
		return operations.NewCommentDeleteOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	// Succeeded
	return operations.NewCommentDeleteOK().WithPayload(&models.APIResponseBase{Success: true})
}

func CommentEdit(params operations.CommentEditParams) middleware.Responder {
	// Find the commenter
	commenter, err := commenterGetByCommenterToken(*params.Body.CommenterToken)
	if err != nil {
		return operations.NewCommentEditOK().WithPayload(&operations.CommentEditOKBody{Message: err.Error()})
	}

	// Find the existing comment
	comment, err := commentGetByCommentHex(*params.Body.CommentHex)
	if err != nil {
		return operations.NewCommentEditOK().WithPayload(&operations.CommentEditOKBody{Message: err.Error()})
	}

	// If not updating their own comment, the user must be a domain moderator
	if comment.CommenterHex != commenter.CommenterHex {
		// Fetch comment's domain
		if domain, _, err := commentDomainPathGet(*params.Body.CommentHex); err != nil {
			return operations.NewCommentEditOK().WithPayload(&operations.CommentEditOKBody{Message: err.Error()})
			// Find the domain moderator
		} else if isModerator, err := isDomainModerator(domain, commenter.Email); err != nil {
			return operations.NewCommentEditOK().WithPayload(&operations.CommentEditOKBody{Message: err.Error()})
			// Check the commenter is a domain moderator
		} else if !isModerator {
			return operations.NewCommentEditOK().WithPayload(&operations.CommentEditOKBody{Message: util.ErrorNotModerator.Error()})
		}
	}

	// Render the comment into HTML
	markdown := swag.StringValue(params.Body.Markdown)
	html := util.MarkdownToHTML(markdown)

	// Persist the comment in the database
	if err := commentEdit(*params.Body.CommentHex, markdown, html); err != nil {
		return operations.NewCommentEditOK().WithPayload(&operations.CommentEditOKBody{Message: err.Error()})
	}

	// Succeeded
	return operations.NewCommentEditOK().WithPayload(&operations.CommentEditOKBody{
		HTML:    html,
		Success: true,
	})
}

func CommentList(params operations.CommentListParams) middleware.Responder {
	domainName := *params.Body.Domain
	domain, err := domainGet(domainName)
	if err != nil {
		return operations.NewCommentListOK().WithPayload(&operations.CommentListOKBody{Message: err.Error()})
	}

	// Fetch the page info
	page, err := pageGet(domainName, params.Body.Path)
	if err != nil {
		return operations.NewCommentListOK().WithPayload(&operations.CommentListOKBody{Message: err.Error()})
	}

	// If it isn't an anonymous token, try to find the related Commenter
	var commenter *models.Commenter
	if *params.Body.CommenterToken != AnonymousCommenterHexID {
		if commenter, err = commenterGetByCommenterToken(*params.Body.CommenterToken); err != nil {
			return operations.NewCommentListOK().WithPayload(&operations.CommentListOKBody{Message: err.Error()})
		}
	}

	// Make a map of moderator emails, also figure out if the user is a moderator self
	isModerator := false
	moderatorEmailMap := map[strfmt.Email]bool{}
	for _, mod := range domain.Moderators {
		moderatorEmailMap[mod.Email] = true
		if commenter != nil && mod.Email == commenter.Email {
			isModerator = true
		}
	}

	// Register a view in domain statistics
	domainViewRecord(domainName, commenter)

	comments, commenters, err := commentList(commenter, domainName, params.Body.Path, isModerator)
	if err != nil {
		return operations.NewCommentListOK().WithPayload(&operations.CommentListOKBody{Message: err.Error()})
	}

	_commenters := map[models.CommenterHexID]*models.Commenter{}
	for ch, cr := range commenters {
		if moderatorEmailMap[cr.Email] {
			cr.IsModerator = true
		}
		cr.Email = ""
		_commenters[ch] = cr
	}

	// Prepare a map of configured identity providers: federated ones should only be enabled when configured
	idps := domain.Idps.Clone()
	for idp, gothIdP := range util.FederatedIdProviders {
		idps[idp] = idps[idp] && goth.GetProviders()[gothIdP] != nil
	}

	return operations.NewCommentListOK().WithPayload(&operations.CommentListOKBody{
		Attributes:            page,
		Commenters:            _commenters,
		Comments:              comments,
		ConfiguredOauths:      idps,
		DefaultSortPolicy:     domain.DefaultSortPolicy,
		Domain:                domainName,
		IsFrozen:              domain.State == models.DomainStateFrozen,
		IsModerator:           isModerator,
		RequireIdentification: domain.RequireIdentification,
		RequireModeration:     domain.RequireModeration,
		Success:               true,
	})
}

func CommentNew(params operations.CommentNewParams) middleware.Responder {
	domainName := *params.Body.Domain
	domain, err := domainGet(domainName)
	if err != nil {
		return operations.NewCommentNewOK().WithPayload(&operations.CommentNewOKBody{Message: err.Error()})
	}

	if domain.State == "frozen" {
		return operations.NewCommentNewOK().WithPayload(&operations.CommentNewOKBody{Message: util.ErrorDomainFrozen.Error()})
	}

	if domain.RequireIdentification && *params.Body.CommenterToken == AnonymousCommenterHexID {
		return operations.NewCommentNewOK().WithPayload(&operations.CommentNewOKBody{Message: util.ErrorNotAuthorised.Error()})
	}

	commenterHex := AnonymousCommenterHexID
	commenterEmail := strfmt.Email("")
	commenterName := "Anonymous"
	commenterLink := ""
	var isModerator bool
	if *params.Body.CommenterToken != AnonymousCommenterHexID {
		c, err := commenterGetByCommenterToken(*params.Body.CommenterToken)
		if err != nil {
			return operations.NewCommentNewOK().WithPayload(&operations.CommentNewOKBody{Message: err.Error()})
		}
		commenterHex = c.CommenterHex
		commenterEmail = c.Email
		commenterName = c.Name
		commenterLink = c.Link
		for _, mod := range domain.Moderators {
			if mod.Email == c.Email {
				isModerator = true
				break
			}
		}
	}

	var state models.CommentState
	if isModerator {
		state = models.CommentStateApproved
	} else if domain.RequireModeration || commenterHex == AnonymousCommenterHexID && domain.ModerateAllAnonymous {
		state = models.CommentStateUnapproved
	} else if domain.AutoSpamFilter && checkForSpam(*params.Body.Domain, util.UserIP(params.HTTPRequest), util.UserAgent(params.HTTPRequest), commenterName, string(commenterEmail), commenterLink, *params.Body.Markdown) {
		state = models.CommentStateFlagged
	} else {
		state = models.CommentStateApproved
	}

	commentHex, err := commentNew(commenterHex, domainName, params.Body.Path, *params.Body.ParentHex, *params.Body.Markdown, state, strfmt.DateTime(time.Now().UTC()))
	if err != nil {
		return operations.NewCommentNewOK().WithPayload(&operations.CommentNewOKBody{Message: err.Error()})
	}

	// TODO: reuse html in commentNew and do only one markdown to HTML conversion?
	html := util.MarkdownToHTML(*params.Body.Markdown)
	go emailNotificationNew(domain, params.Body.Path, commenterHex, commentHex, html, *params.Body.ParentHex, state)

	// Succeeded
	return operations.NewCommentNewOK().WithPayload(&operations.CommentNewOKBody{
		CommentHex: commentHex,
		HTML:       html,
		State:      state,
		Success:    true,
	})
}

func CommentVote(params operations.CommentVoteParams) middleware.Responder {
	if *params.Body.CommenterToken == AnonymousCommenterHexID {
		return operations.NewCommentVoteOK().WithPayload(&models.APIResponseBase{Message: util.ErrorUnauthorisedVote.Error()})
	}

	c, err := commenterGetByCommenterToken(*params.Body.CommenterToken)
	if err != nil {
		return operations.NewCommentVoteOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	direction := 0
	if *params.Body.Direction > 0 {
		direction = 1
	} else if *params.Body.Direction < 0 {
		direction = -1
	}

	if err := commentVote(c.CommenterHex, *params.Body.CommentHex, direction); err != nil {
		return operations.NewCommentVoteOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	// Succeeded
	return operations.NewCommentVoteOK().WithPayload(&models.APIResponseBase{Success: true})
}

func commentApprove(commentHex models.HexID) error {
	if commentHex == "" {
		return util.ErrorMissingField
	}

	_, err := svc.DB.Exec("update comments set state = 'approved' where commentHex = $1;", commentHex)
	if err != nil {
		logger.Errorf("cannot approve comment: %v", err)
		return util.ErrorInternal
	}

	return nil
}

func commentCount(domain string, paths []string) (map[string]int, error) {
	commentCounts := map[string]int{}

	if domain == "" {
		return nil, util.ErrorMissingField
	}

	if len(paths) == 0 {
		return nil, util.ErrorEmptyPaths
	}

	rows, err := svc.DB.Query(
		"select path, commentCount from pages where domain = $1 and path = any($2);",
		domain,
		pq.Array(paths))
	if err != nil {
		logger.Errorf("cannot get comments: %v", err)
		return nil, util.ErrorInternal
	}
	defer rows.Close()

	for rows.Next() {
		var path string
		var commentCount int
		if err = rows.Scan(&path, &commentCount); err != nil {
			logger.Errorf("cannot scan path and commentCount: %v", err)
			return nil, util.ErrorInternal
		}

		commentCounts[path] = commentCount
	}

	return commentCounts, nil
}

func commentDelete(commentHex models.HexID, deleterHex models.HexID) error {
	if commentHex == "" || deleterHex == "" {
		return util.ErrorMissingField
	}

	_, err := svc.DB.Exec(
		"update comments "+
			"set deleted = true, markdown = '[deleted]', html = '[deleted]', commenterHex = 'anonymous', deleterHex = $2, deletionDate = $3 "+
			"where commentHex = $1;",
		commentHex,
		deleterHex,
		time.Now().UTC(),
	)

	if err != nil {
		// TODO: make sure this is the error is actually nonexistent commentHex
		return util.ErrorNoSuchComment
	}

	return nil
}

func commentDomainPathGet(commentHex models.HexID) (string, string, error) {
	if commentHex == "" {
		return "", "", util.ErrorMissingField
	}

	row := svc.DB.QueryRow("select domain, path from comments where commentHex=$1;", commentHex)

	var domain string
	var path string
	var err error
	if err = row.Scan(&domain, &path); err != nil {
		return "", "", util.ErrorNoSuchDomain
	}

	return domain, path, nil
}

// commentEdit updates the comment with the given hex ID in the database
func commentEdit(commentHex models.HexID, markdown, html string) error {
	if _, err := svc.DB.Exec("update comments set markdown=$2, html=$3 where commentHex=$1;", commentHex, markdown, html); err != nil {
		// TODO: make sure this is the error is actually nonexistent commentHex
		return util.ErrorNoSuchComment
	}

	return nil
}

func commentGetByCommentHex(commentHex models.HexID) (*models.Comment, error) {
	if commentHex == "" {
		return nil, util.ErrorMissingField
	}

	row := svc.DB.QueryRow(fmt.Sprintf("select %s from comments where comments.commentHex=$1;", commentsRowColumns), commentHex)
	var comment models.Comment
	if err := commentsRowScan(row, &comment); err != nil {
		// TODO: is this the only error?
		return nil, util.ErrorNoSuchComment
	}

	return &comment, nil
}

func commentList(commenter *models.Commenter, domain string, path string, isModerator bool) ([]*models.Comment, map[models.CommenterHexID]*models.Commenter, error) {
	// Prepare a query
	statement := "select commentHex, commenterHex, markdown, html, parentHex, score, state, deleted, creationDate " +
		"from comments " +
		"where comments.domain=$1 and comments.path=$2 and comments.deleted=false"
	params := []any{domain, path}

	// If the commenter is no moderator, show all unapproved comments
	if !isModerator {
		// Anonymous commenter: only include approved
		if commenter == nil {
			statement += " and comments.state='approved'"

		} else {
			// Authenticated commenter: also show their own unapproved comments
			statement += " and (comments.state='approved' or comments.commenterHex=$3)"
			params = append(params, commenter.CommenterHex)
		}
	}
	statement += `;`

	// Fetch the comments
	rows, err := svc.DB.Query(statement, params...)
	if err != nil {
		logger.Errorf("cannot get comments: %v", err)
		return nil, nil, util.ErrorInternal
	}
	defer rows.Close()

	commenters := map[models.CommenterHexID]*models.Commenter{
		AnonymousCommenterHexID: {
			CommenterHex: AnonymousCommenterHexID,
			Email:        "undefined",
			Name:         "Anonymous",
			Link:         "undefined",
			Photo:        "undefined",
			Provider:     "undefined",
		},
	}

	var comments []*models.Comment
	for rows.Next() {
		comment := models.Comment{}
		if err = rows.Scan(
			&comment.CommentHex,
			&comment.CommenterHex,
			&comment.Markdown,
			&comment.HTML,
			&comment.ParentHex,
			&comment.Score,
			&comment.State,
			&comment.Deleted,
			&comment.CreationDate); err != nil {
			return nil, nil, util.ErrorInternal
		}

		// If it's an authenticated commenter, load their comment votes
		if commenter != nil {
			row := svc.DB.QueryRow(
				"select direction from votes where commentHex=$1 and commenterHex=$2;",
				comment.CommentHex,
				commenter.CommenterHex)
			if err = row.Scan(&comment.Direction); err != nil {
				// TODO: is the only error here that there is no such entry?
				comment.Direction = 0
			}
		}

		// Do not include the original markdown for anonymous and other commenters, unless it's a moderator
		if commenter == nil || !isModerator && commenter.CommenterHex != comment.CommenterHex {
			comment.Markdown = ""
		}

		// Also, do not report comment state for non-moderators
		if !isModerator {
			comment.State = ""
		}

		// Append the comment to the list
		comments = append(comments, &comment)

		// Add the commenter to the map
		// TODO OMG this must be sloooooooow
		if _, ok := commenters[comment.CommenterHex]; !ok {
			commenters[comment.CommenterHex], err = commenterGetByHex(comment.CommenterHex)
			if err != nil {
				logger.Errorf("cannot retrieve commenter: %v", err)
				return nil, nil, util.ErrorInternal
			}
		}
	}

	// Succeeded
	return comments, commenters, nil
}

// Take `creationDate` as a param because comment import (from Disqus, for example) will require a custom time
func commentNew(commenterHex models.CommenterHexID, domain string, path string, parentHex models.ParentHexID, markdown string, state models.CommentState, creationDate strfmt.DateTime) (models.HexID, error) {
	// path is allowed to be empty
	if commenterHex == "" || domain == "" || parentHex == "" || markdown == "" || state == "" {
		return "", util.ErrorMissingField
	}

	p, err := pageGet(domain, path)
	if err != nil {
		logger.Errorf("cannot get page attributes: %v", err)
		return "", util.ErrorInternal
	}

	if p.IsLocked {
		return "", util.ErrorThreadLocked
	}

	commentHex, err := util.RandomHex(32)
	if err != nil {
		return "", err
	}

	html := util.MarkdownToHTML(markdown)

	if err = pageNew(domain, path); err != nil {
		return "", err
	}

	_, err = svc.DB.Exec(
		"insert into comments(commentHex, domain, path, commenterHex, parentHex, markdown, html, creationDate, state) "+
			"values($1, $2, $3, $4, $5, $6, $7, $8, $9);",
		commentHex,
		domain,
		path,
		commenterHex,
		parentHex,
		markdown,
		html,
		creationDate,
		state)
	if err != nil {
		logger.Errorf("cannot insert comment: %v", err)
		return "", util.ErrorInternal
	}

	return models.HexID(commentHex), nil
}

func commentsRowScan(s util.Scanner, c *models.Comment) error {
	return s.Scan(
		&c.CommentHex,
		&c.CommenterHex,
		&c.Markdown,
		&c.HTML,
		&c.ParentHex,
		&c.Score,
		&c.State,
		&c.Deleted,
		&c.CreationDate,
	)
}

func commentVote(commenterHex models.CommenterHexID, commentHex models.HexID, direction int) error {
	if commentHex == "" || commenterHex == "" {
		return util.ErrorMissingField
	}

	row := svc.DB.QueryRow("select commenterHex from comments where commentHex = $1;", commentHex)

	var authorHex models.CommenterHexID
	if err := row.Scan(&authorHex); err != nil {
		logger.Errorf("error selecting authorHex for vote")
		return util.ErrorInternal
	}

	if authorHex == commenterHex {
		return util.ErrorSelfVote
	}

	_, err := svc.DB.Exec(
		"insert into votes(commentHex, commenterHex, direction, voteDate) values($1, $2, $3, $4) "+
			"on conflict (commentHex, commenterHex) do update set direction = $3;",
		commentHex,
		commenterHex,
		direction,
		time.Now().UTC())
	if err != nil {
		logger.Errorf("error inserting/updating votes: %v", err)
		return util.ErrorInternal
	}

	return nil
}
