package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
	"github.com/ttab/elephant-api/index"
	"github.com/ttab/elephant-api/newsdoc"
	"github.com/ttab/elephant-api/repository"
	"github.com/ttab/elephantine"
	"github.com/ttab/flerr"
	"github.com/twitchtv/twirp"
	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/encoding/protojson"
)

func uploadDoc(ctx context.Context, cmd *cli.Command) error {
	var (
		env     = cmd.String("env")
		ifMatch = cmd.Int64("if-match")
	)

	clients, err := getClients(ctx, env)
	if err != nil {
		return err
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read input document: %w", err)
	}

	var doc newsdoc.Document

	err = protojson.Unmarshal(data, &doc)
	if err != nil {
		return fmt.Errorf("unmarshal input document: %w", err)
	}

	res, err := clients.Documents.Update(ctx,
		&repository.UpdateRequest{
			Uuid:     doc.Uuid,
			IfMatch:  ifMatch,
			Document: &doc,
		})
	if err != nil {
		return fmt.Errorf("upload doc: %w", err)
	}

	resData, err := protojson.Marshal(res)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	_, err = os.Stdout.Write(resData)
	if err != nil {
		return fmt.Errorf("write response to stdout: %w", err)
	}

	return nil
}

func setStatus(ctx context.Context, cmd *cli.Command) error {
	var (
		env        = cmd.String("env")
		docUUID    = cmd.String("uuid")
		statusName = cmd.String("status")
		version    = cmd.Int64("version")
		meta       = cmd.StringSlice("meta")
	)

	clients, err := getClients(ctx, env)
	if err != nil {
		return err
	}

	if version == 0 {
		meta, err := clients.Documents.GetMeta(ctx, &repository.GetMetaRequest{
			Uuid: docUUID,
		})
		if err != nil {
			return fmt.Errorf("get current version: %w", err)
		}

		version = meta.Meta.CurrentVersion
	}

	status := repository.StatusUpdate{
		Name:    statusName,
		Version: version,
		Meta:    map[string]string{},
	}

	for _, ms := range meta {
		key, value, ok := strings.Cut(ms, "=")
		if !ok {
			return fmt.Errorf("invalid meta value %q", ms)
		}

		status.Meta[key] = value
	}

	req := repository.UpdateRequest{
		Uuid:   docUUID,
		Status: []*repository.StatusUpdate{&status},
	}

	_, err = clients.Documents.Update(ctx, &req)
	if err != nil {
		return fmt.Errorf("set status: %w", err)
	}

	return nil
}

func queryIndex(ctx context.Context, cmd *cli.Command) error {
	env := cmd.String("env")

	clients, err := getClients(ctx, env)
	if err != nil {
		return err
	}

	var buf bytes.Buffer

	tee := io.TeeReader(os.Stdin, &buf)

	dec := json.NewDecoder(tee)

	var dummyQuery struct{}

	// Using decode as a hack to consume the full JSON object from stdin and
	// thus teeing it to the buffer for proper decode.
	err = dec.Decode(&dummyQuery)
	if err != nil {
		return fmt.Errorf("read query from input: %w", err)
	}

	var q index.QueryRequestV1

	err = protojson.Unmarshal(buf.Bytes(), &q)
	if err != nil {
		return fmt.Errorf("decode query from input: %w", err)
	}

	if len(q.Sort) == 0 {
		q.Sort = append(q.Sort, &index.SortingV1{
			Field: "_id",
		})
	}

	for {
		res, err := clients.Search.Query(ctx, &q)
		if err != nil {
			return fmt.Errorf("list document UUIDs: %w", err)
		}

		if len(res.Hits.Hits) == 0 {
			break
		}

		for _, hit := range res.Hits.Hits {
			_, _ = fmt.Fprintln(os.Stdout, hit.Id)

			q.SearchAfter = hit.Sort
		}
	}

	return nil
}

func listDocuments(ctx context.Context, cmd *cli.Command) error {
	var (
		env      = cmd.String("env")
		docType  = cmd.String("doc-type")
		maxCount = cmd.Int("max")
	)

	babby, err := getClients(ctx, env)
	if err != nil {
		return err
	}

	q := index.QueryRequestV1{
		DocumentType: docType,
		Query:        index.MatchAllQuery(),
		Sort: []*index.SortingV1{
			{Field: "_id"},
		},
		Fields: []string{
			"document.title", "current_version", "document.uri",
		},
	}

	fmt.Println(strings.Join([]string{
		"uuid", "current_version", "uri", "title",
	}, "\t"))

	var count int

	for {
		res, err := babby.Search.Query(ctx, &q)
		if err != nil {
			return fmt.Errorf("list document UUIDs: %w", err)
		}

		if len(res.Hits.Hits) == 0 {
			break
		}

		for _, hit := range res.Hits.Hits {
			count++

			fmt.Println(strings.Join([]string{
				hit.Id,
				hit.GetField("current_version"),
				hit.GetField("document.uri"),
				hit.GetField("document.title"),
			}, "\t"))

			q.SearchAfter = hit.Sort
		}

		if count > maxCount {
			break
		}
	}

	return nil
}

func copyDocument(ctx context.Context, cmd *cli.Command) (outErr error) {
	var (
		fromEnv    = cmd.String("env")
		toEnv      = cmd.String("to-env")
		docID      = cmd.String("uuid")
		attachment = cmd.String("attachment")
		statusName = cmd.String("status")
		access     = cmd.StringSlice("access")
	)

	acl, err := buildACL(access)
	if err != nil {
		return fmt.Errorf("parse access: %w", err)
	}

	from, err := getClients(ctx, fromEnv)
	if err != nil {
		return err
	}

	to, err := getClients(ctx, toEnv)
	if err != nil {
		return err
	}

	err = copySingleDocument(ctx,
		docID, attachment, statusName, acl,
		from.Documents, to.Documents)
	if err != nil {
		return err
	}

	return nil
}

func copyDocuments(ctx context.Context, cmd *cli.Command) (outErr error) {
	var (
		fromEnv    = cmd.String("env")
		toEnv      = cmd.String("to-env")
		docType    = cmd.String("doc-type")
		skipDocs   = cmd.Int("skip")
		attachment = cmd.String("attachment")
		statusName = cmd.String("status")
		access     = cmd.StringSlice("access")
	)

	acl, err := buildACL(access)
	if err != nil {
		return fmt.Errorf("parse access: %w", err)
	}

	from, err := getClients(ctx, fromEnv)
	if err != nil {
		return err
	}

	to, err := getClients(ctx, toEnv)
	if err != nil {
		return err
	}

	var list []string

	q := index.QueryRequestV1{
		DocumentType: docType,
		Query:        index.MatchAllQuery(),
		Sort: []*index.SortingV1{
			{Field: "_id"},
		},
	}

	for {
		res, err := from.Search.Query(ctx, &q)
		if err != nil {
			return fmt.Errorf("list document UUIDs: %w", err)
		}

		if len(res.Hits.Hits) == 0 {
			break
		}

		for _, hit := range res.Hits.Hits {
			list = append(list, hit.Id)
			q.SearchAfter = hit.Sort
		}
	}

	origLen := len(list)

	if skipDocs >= origLen {
		println("All documents skipped")
	}

	list = list[skipDocs:]

	ok := askForConfirmation(fmt.Sprintf("Copy %d documents", len(list)))
	if !ok {
		return nil
	}

	if len(list) > 500 {
		ok := askForConfirmation("Are you SURE? That is a lot of documents.")
		if !ok {
			return nil
		}
	}

	bar := progressbar.Default(int64(origLen))

	_ = bar.Add(skipDocs)

	for _, docID := range list {
		err := copySingleDocument(
			ctx,
			docID, attachment, statusName, acl,
			from.Documents, to.Documents)
		if err != nil {
			return err
		}

		_ = bar.Add(1)
	}

	return nil
}

func copySingleDocument(
	ctx context.Context,
	docID string,
	attachment string,
	statusName string,
	acl []*repository.ACLEntry,
	from repository.Documents,
	to repository.Documents,
) error {
	docRes, err := from.Get(ctx,
		&repository.GetDocumentRequest{
			Uuid: docID,
		})
	if err != nil {
		return fmt.Errorf("read document %s: %w", docID, err)
	}

	attach := make(map[string]string)

	if attachment == "" {
		attachment = knownAttachments[docRes.Document.Type]
	}

	if attachment != "" {
		uploadID, err := transferAttachment(ctx, docID, attachment, from, to)
		if err != nil {
			return err
		}

		if uploadID != "" {
			attach[attachment] = uploadID
		}
	}

	req := repository.UpdateRequest{
		Uuid:          docID,
		Document:      docRes.Document,
		AttachObjects: attach,
		Acl:           acl,
	}

	if statusName != "" {
		req.Status = []*repository.StatusUpdate{
			{Name: statusName},
		}
	}

	_, err = to.Update(ctx, &req)
	if err != nil {
		return fmt.Errorf("write document %s: %w", docID, err)
	}

	return nil
}

func aclUpdate(ctx context.Context, cmd *cli.Command) error {
	var (
		env     = cmd.String("env")
		docUUID = cmd.String("uuid")
		docType = cmd.String("doc-type")
		access  = cmd.StringSlice("access")
	)

	if (docUUID == "") == (docType == "") {
		return errors.New("either --uuid or --doc-type must be supplied")
	}

	if len(access) == 0 {
		return errors.New("at least one --access entry must be supplied")
	}

	requested, err := buildACL(access)
	if err != nil {
		return fmt.Errorf("parse access: %w", err)
	}

	clients, err := getClients(ctx, env)
	if err != nil {
		return err
	}

	if docUUID != "" {
		changed, err := ensureACL(ctx, clients.Documents, docUUID, requested)
		if err != nil {
			return err
		}

		if changed {
			fmt.Println("ACL updated")
		} else {
			fmt.Println("ACL already up to date")
		}

		return nil
	}

	var list []string

	q := index.QueryRequestV1{
		DocumentType: docType,
		Query:        index.MatchAllQuery(),
		Sort: []*index.SortingV1{
			{Field: "_id"},
		},
	}

	for {
		res, err := clients.Search.Query(ctx, &q)
		if err != nil {
			return fmt.Errorf("list document UUIDs: %w", err)
		}

		if len(res.Hits.Hits) == 0 {
			break
		}

		for _, hit := range res.Hits.Hits {
			list = append(list, hit.Id)
			q.SearchAfter = hit.Sort
		}
	}

	ok := askForConfirmation(
		fmt.Sprintf("Update ACL on %d documents", len(list)))
	if !ok {
		return nil
	}

	if len(list) > 500 {
		ok := askForConfirmation("Are you SURE? That is a lot of documents.")
		if !ok {
			return nil
		}
	}

	bar := progressbar.Default(int64(len(list)))

	for _, id := range list {
		_, err := ensureACL(ctx, clients.Documents, id, requested)
		if err != nil {
			return err
		}

		_ = bar.Add(1)
	}

	return nil
}

func ensureACL(
	ctx context.Context,
	docs repository.Documents,
	docID string,
	requested []*repository.ACLEntry,
) (bool, error) {
	meta, err := docs.GetMeta(ctx, &repository.GetMetaRequest{
		Uuid: docID,
	})
	if err != nil {
		return false, fmt.Errorf("get meta for %s: %w", docID, err)
	}

	existing := make(map[string]map[string]bool, len(meta.Meta.Acl))

	for _, e := range meta.Meta.Acl {
		perms := make(map[string]bool, len(e.Permissions))

		for _, p := range e.Permissions {
			perms[p] = true
		}

		existing[e.Uri] = perms
	}

	var updates []*repository.ACLEntry

	for _, r := range requested {
		current := existing[r.Uri]

		needsUpdate := false

		for _, p := range r.Permissions {
			if !current[p] {
				needsUpdate = true

				break
			}
		}

		if !needsUpdate {
			continue
		}

		merged := make(map[string]bool, len(current)+len(r.Permissions))

		for p := range current {
			merged[p] = true
		}

		for _, p := range r.Permissions {
			merged[p] = true
		}

		perms := make([]string, 0, len(merged))

		for p := range merged {
			perms = append(perms, p)
		}

		slices.Sort(perms)

		updates = append(updates, &repository.ACLEntry{
			Uri:         r.Uri,
			Permissions: perms,
		})
	}

	if len(updates) == 0 {
		return false, nil
	}

	_, err = docs.Update(ctx, &repository.UpdateRequest{
		Uuid: docID,
		Acl:  updates,
	})
	if err != nil {
		return false, fmt.Errorf("update ACL for %s: %w", docID, err)
	}

	return true, nil
}

func buildACL(entries []string) ([]*repository.ACLEntry, error) {
	if len(entries) == 0 {
		return []*repository.ACLEntry{
			{
				Uri:         "core://unit/redaktionen",
				Permissions: []string{"r", "w"},
			},
		}, nil
	}

	acl := make([]*repository.ACLEntry, 0, len(entries))

	for _, e := range entries {
		perms, uri, ok := strings.Cut(e, ":")
		if !ok {
			return nil, fmt.Errorf(
				"invalid access entry %q: expected 'perms:uri'", e)
		}

		if perms == "" {
			return nil, fmt.Errorf(
				"invalid access entry %q: missing permissions", e)
		}

		if uri == "" {
			return nil, fmt.Errorf(
				"invalid access entry %q: missing uri", e)
		}

		permList := make([]string, 0, len(perms))

		for _, p := range perms {
			permList = append(permList, string(p))
		}

		acl = append(acl, &repository.ACLEntry{
			Uri:         uri,
			Permissions: permList,
		})
	}

	return acl, nil
}

var knownAttachments = map[string]string{
	"core/image":      "image",
	"tt/tv-channel":   "logo",
	"tt/print-layout": "layout",
}

func transferAttachment(
	ctx context.Context,
	docID string, attachmentName string,
	from, to repository.Documents,
) (_ string, outErr error) {
	// TODO: Add method for getting all attachments for a single
	// document. GetAttachments is for focused consumption in bulk.
	source, err := from.GetAttachments(ctx,
		&repository.GetAttachmentsRequest{
			Documents:      []string{docID},
			AttachmentName: attachmentName,
			DownloadLink:   true,
		})
	if err != nil {
		return "", fmt.Errorf("get attachments: %w", err)
	}

	var attachment *repository.AttachmentDetails

	for _, a := range source.Attachments {
		if a.Document != docID {
			continue
		}

		attachment = a

		break
	}

	if attachment == nil {
		return "", nil
	}

	res, err := http.Get(attachment.DownloadLink)
	if err != nil {
		return "", fmt.Errorf("make download request: %w", err)
	}

	defer elephantine.Close("download body", res.Body, &outErr)

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"error response when requesting object: %s",
			res.Status)
	}

	// TODO: use temp files, this assumes small-ish attachments.
	var buf bytes.Buffer

	size, err := io.Copy(&buf, res.Body)
	if err != nil {
		return "", fmt.Errorf("download object data: %w", err)
	}

	createRes, err := to.CreateUpload(ctx, &repository.CreateUploadRequest{
		Name:        attachment.Filename,
		ContentType: attachment.ContentType,
		// TODO: Missing metadata from AttachmentDetails.
	})
	if err != nil {
		return "", fmt.Errorf("create upload: %w", err)
	}

	uploadReq, err := http.NewRequestWithContext(ctx,
		http.MethodPut, createRes.Url, &buf)
	if err != nil {
		return "", fmt.Errorf("create upload request: %w", err)
	}

	uploadReq.ContentLength = size

	uploadRes, err := http.DefaultClient.Do(uploadReq)
	if err != nil {
		return "", fmt.Errorf("perform upload request: %w", err)
	}

	if uploadRes.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"error response when uploading object: %s",
			uploadRes.Status)
	}

	return createRes.Id, nil
}

func askForConfirmation(s string) bool {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("%s [y/n]: ", s)

		response, err := reader.ReadString('\n')
		if err != nil {
			println(err.Error())

			return false
		}

		response = strings.ToLower(strings.TrimSpace(response))

		switch response {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}

func listDocVersions(ctx context.Context, cmd *cli.Command) (outErr error) {
	var (
		env     = cmd.String("env")
		docUUID = cmd.String("uuid")
		uri     = cmd.String("uri")
		before  = cmd.Int64("before")
	)

	if docUUID == "" && uri == "" {
		return errors.New("either supply a uuid or uri")
	}

	if docUUID == "" {
		cu := uuid.NewSHA1(uuid.NameSpaceURL, []byte(uri))

		docUUID = cu.String()
	}

	babby, err := getClients(ctx, env)
	if err != nil {
		return err
	}

	hist, err := babby.Documents.GetHistory(ctx,
		&repository.GetHistoryRequest{
			Uuid:   docUUID,
			Before: before,
		})
	if err != nil {
		return fmt.Errorf("fetch document history: %w", err)
	}

	creators := make(map[string]string)

	for _, item := range hist.Versions {
		creatorName, ok := creators[item.Creator]
		if !ok {
			creatorName = resolveUser(ctx, babby.Search, item.Creator)

			creators[item.Creator] = creatorName
		}

		if creatorName == "" {
			creatorName = "Unknown"
		}

		fmt.Printf("* Version: %d\n", item.Version)
		fmt.Printf("  Created: %s\n", item.Created)
		fmt.Printf("  Creator: %s (%s)\n", creatorName, item.Creator)
		println()
	}

	return nil
}

func resolveUser(
	ctx context.Context,
	search index.SearchV1,
	sub string,
) string {
	altSub := strings.Replace(sub, "core://user/", "core://user/sub/", 1)

	hits, err := search.Query(ctx, &index.QueryRequestV1{
		DocumentType: "core/author",
		Fields:       []string{"document.title"},
		Query: index.BoolQuery(&index.BoolQueryV1{
			Should: []*index.QueryV1{
				index.TermQuery(&index.TermQueryV1{
					Field: "document.rel.same_as.uri",
					Value: sub,
				}),
				index.TermQuery(&index.TermQueryV1{
					Field: "document.rel.same_as.uri",
					Value: altSub,
				}),
			},
		}),
		Sort: []*index.SortingV1{{
			Field: "heads.usable.version",
			Desc:  true,
		}},
	})
	if err != nil {
		println(err.Error())
		return ""
	}

	if len(hits.Hits.Hits) == 0 {
		return ""
	}

	return hits.Hits.Hits[0].GetField("document.title")
}

func downloadDoc(ctx context.Context, cmd *cli.Command) (outErr error) {
	var (
		env     = cmd.String("env")
		docUUID = cmd.String("uuid")
		out     = cmd.String("out")
		uri     = cmd.String("uri")
		version = cmd.Int64("version")
	)

	if docUUID == "" && uri == "" {
		return errors.New("either supply a uuid or uri")
	}

	if docUUID == "" {
		cu := uuid.NewSHA1(uuid.NameSpaceURL, []byte(uri))

		docUUID = cu.String()
	}

	var clean flerr.Cleaner

	defer clean.FlushTo(&outErr)

	babby, err := getClients(ctx, env)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	docRes, err := babby.Documents.Get(ctx, &repository.GetDocumentRequest{
		Uuid:    docUUID,
		Version: version,
	})
	if err != nil {
		return fmt.Errorf("get document: %w", err)
	}

	var w io.Writer = os.Stdout

	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}

		clean.Addf(f.Close, "close output file")

		w = f
	}

	opt := protojson.MarshalOptions{
		Indent: "  ",
	}

	data, err := opt.Marshal(docRes.Document)
	if err != nil {
		return fmt.Errorf("marshal document to JSON: %w", err)
	}

	_, err = w.Write(data)
	if err != nil {
		return fmt.Errorf("write document to file: %w", err)
	}

	return nil
}

func runListAction(ctx context.Context, cmd *cli.Command) (outErr error) {
	var (
		env           = cmd.String("env")
		sep           = cmd.String("separator")
		idField       = cmd.String("id-field")
		selectedField = cmd.String("selected-field")
		unpublish     = cmd.Bool("unpublish")
	)

	// Only allow one action to avoid any surprises.
	if !oneTrue(unpublish) {
		return errors.New("a single action needs to be specified")
	}

	sepRunes := []rune(sep)

	fmt.Printf("%q %v\n", sep, sepRunes)

	if len(sepRunes) != 1 {
		return errors.New("separator must be a single character")
	}

	if len(idField) == 0 {
		return errors.New("id-field cannot be empty")
	}

	if !cmd.Args().Present() {
		return errors.New("missing CSV file argument")
	}

	f, err := os.Open(cmd.Args().First())
	if err != nil {
		return fmt.Errorf("open CSV file: %w", err)
	}

	defer elephantine.Close("CSV file", f, &outErr)

	lines, err := lineCounter(f)
	if err != nil {
		return fmt.Errorf("count lines in CSV: %w", err)
	}

	_, err = f.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("seek to beginning of file: %w", err)
	}

	clients, err := getClients(ctx, env)
	if err != nil {
		return err
	}

	r := csv.NewReader(f)

	r.Comma = sepRunes[0]

	headerRow, err := r.Read()
	if err != nil {
		return fmt.Errorf("read header row: %w", err)
	}

	idIndex := slices.Index(headerRow, idField)
	if idIndex == -1 {
		return fmt.Errorf(
			"could not find id column %q, is there a header row?",
			idField)
	}

	selectedIndex := -1

	if selectedField != "" {
		idx := slices.Index(headerRow, selectedField)
		if idx == -1 {
			return fmt.Errorf("could not find 'selected' field %q",
				selectedField)
		}

		selectedIndex = idx
	}

	line := 1

	bar := progressbar.Default(int64(lines - 1))

	for {
		line++

		_ = bar.Add(1)

		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("read CSV row %d: %w", line, err)
		}

		if selectedIndex != -1 {
			selected, err := strconv.ParseBool(row[selectedIndex])
			if err != nil {
				return fmt.Errorf(
					"invalid selected value for row %d: %w",
					line, err)
			}

			if !selected {
				continue
			}
		}

		docID := row[idIndex]
		if docID == "" {
			continue
		}

		switch {
		case unpublish:
			err := documentUnpublishAction(ctx, clients, docID)
			if err != nil {
				return fmt.Errorf(
					"unpublish document %s on row %d: %w",
					docID, line, err)
			}
		}
	}

	return nil
}

func lineCounter(r io.Reader) (int, error) {
	buf := make([]byte, 32*1024)
	count := 0
	lineSep := []byte{'\n'}

	for {
		c, err := r.Read(buf)
		count += bytes.Count(buf[:c], lineSep)

		switch {
		case err == io.EOF:
			return count, nil
		case err != nil:
			return count, fmt.Errorf("read from file: %w", err)
		}
	}
}

func documentUnpublishAction(
	ctx context.Context, clients *elephantClients, id string,
) error {
	statusRes, err := clients.Documents.GetStatus(ctx,
		&repository.GetStatusRequest{
			Uuid: id,
			Name: "usable",
		})
	if elephantine.IsTwirpErrorCode(err, twirp.NotFound) {
		// Either there's no such document, or it hasn't had any usable
		// status set. Either way, mission accomplished, no?
		return nil
	} else if err != nil {
		return fmt.Errorf("get current usable status: %w", err)
	}

	if statusRes.Status.Version == -1 {
		// Already unpublished.
		return nil
	}

	_, err = clients.Documents.Update(ctx, &repository.UpdateRequest{
		Uuid: id,
		Status: []*repository.StatusUpdate{
			{
				Name:    "usable",
				Version: -1,
				IfMatch: statusRes.Status.Id,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("set usable to -1: %w", err)
	}

	return nil
}

func oneTrue(b ...bool) bool {
	var v bool

	for i := range b {
		if v && b[i] {
			return false
		}

		v = v || b[i]
	}

	return v
}
