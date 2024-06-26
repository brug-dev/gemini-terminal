package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type GeminiClient struct {
	chatID      int
	client      *genai.Client
	model       *genai.GenerativeModel
	chatSession *genai.ChatSession
	ctx         context.Context
	conf        GeminiChatConfig
}

func newGeminiClient(ctx context.Context, chatID int, conf GeminiChatConfig) (*GeminiClient, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(conf.GoogleAIKey))
	if err != nil {
		return nil, err
	}
	model := client.GenerativeModel(conf.ModelName)
	model.SafetySettings = []*genai.SafetySetting{
		{
			Category: genai.HarmCategoryHarassment,
			Threshold: genai.HarmBlockNone,
		},
		{
			Category: genai.HarmCategoryHateSpeech,
			Threshold: genai.HarmBlockNone,
		},
		{
			Category: genai.HarmCategorySexuallyExplicit,
			Threshold: genai.HarmBlockNone,
		},
		{
			Category: genai.HarmCategoryDangerousContent,
			Threshold: genai.HarmBlockNone,
		},
	}
	return &GeminiClient{
		chatID: chatID,
		client: client,
		ctx:    ctx,
		conf:   conf,
		model:  model,
	}, nil
}

func (g *GeminiClient) startChat(history []*genai.Content) {
	cs := g.model.StartChat()
	if cs == nil {
		log.Fatal("Chat session is nil")
	}
	cs.History = history
	g.chatSession = cs
}

func (g *GeminiClient) sendMessageStream(text string) *genai.GenerateContentResponseIterator {
	prompt := genai.Text(text)
	iter := g.chatSession.SendMessageStream(g.ctx, prompt)
	return iter
}

func (g *GeminiClient) genTitle() string {
	// 生成对话标题prompt
	promtp := "Generate title based on my question, Please give the title directly without any additional explanation or additional characters."
	iter := g.sendMessageStream(promtp)
	modelPart := make([]string, 0)
	for {
		resp, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Println(err.Error())
			break
		}
		if resp != nil &&
			len(resp.Candidates) > 0 &&
			resp.Candidates[0].Content != nil &&
			len(resp.Candidates[0].Content.Parts) > 0 {
			p := fmt.Sprint(resp.Candidates[0].Content.Parts[0])
			modelPart = append(modelPart, p)
		}
	}
	return strings.ReplaceAll(strings.Join(modelPart, ""), "*", "")
}

func (g *GeminiClient) sendMessageToTui(textChan chan string, historyChan chan string, genFlagChan chan bool, titleChan chan string, db *DB) {
	firstQuestion := true
	for {
		text := <-textChan
		historyChan <- "[red]Q:" + text + "\n"
		tx, err := db.SqliteDB.Begin()
		if err != nil {
			log.Fatal(err)
		}
		userPromptArr := []string{text}
		jarr, err := json.Marshal(userPromptArr)
		if err != nil {
			log.Fatal(err)
		}
		err = db.InsertHistoryWithTX(tx, GeminiChatHistory{
			ChatID: int64(g.chatID),
			Prompt: string(jarr),
			Role:   "user",
		})
		if err != nil {
			log.Fatal(err)
		}

		iter := g.sendMessageStream(text)
		modelPart := make([]string, 0)
		historyChan <- "[green]A: "
		for {
			resp, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Println(err.Error())
				break
			}
			if resp != nil &&
				len(resp.Candidates) > 0 &&
				resp.Candidates[0].Content != nil &&
				len(resp.Candidates[0].Content.Parts) > 0 {
				p := fmt.Sprint(resp.Candidates[0].Content.Parts[0])
				modelPart = append(modelPart, p)
				historyChan <- p
			}
		}
		historyChan <- "\n"
		genFlagChan <- false
		modelArr, err := json.Marshal(modelPart)
		if err != nil {
			log.Fatal(err)
		}
		err = db.InsertHistoryWithTX(tx, GeminiChatHistory{
			ChatID: int64(g.chatID),
			Prompt: string(modelArr),
			Role:   "model",
		})
		if err != nil {
			log.Fatal(err)
		}
		tx.Commit()
		if firstQuestion {
			// TODO 插入标题
			firstQuestion = false
			go func() {
				title := g.genTitle()
				titleChan <- title
				db.InsertChat(GeminiChatList{
					ChatID:    int64(g.chatID),
					ChatTitle: title,
				})
			}()
		}

	}
}
