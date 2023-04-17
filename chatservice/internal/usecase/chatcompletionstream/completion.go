package chatcompletionstream

import (
	"context"
	"errors"
	"io"
	"strings"

	openai "github.com/sashabaranov/go-openai"
	"github.com/yurialvesbrasil/fullcyclefx/chatservice/internal/domain/entity"
	"github.com/yurialvesbrasil/fullcyclefx/chatservice/internal/domain/gateway"
)

type ChatCompletionConfigInputDTO struct {
	Model                string
	ModelMaxTokens       int
	Temperature          float32
	TopP                 float32
	N                    int
	Stop                 []string
	MaxTokens            int
	PresencePenalty      float32
	FrequencyPenalty     float32
	InitialSystemMessage string
}

type ChatCompletionInputDTO struct {
	ChatID      string
	UserID      string
	UserMessage string
	Config      ChatCompletionConfigInputDTO
}

type ChatCompletionOuputDTO struct {
	ChatID  string
	UserID  string
	Content string
}

type ChatCompletionUseCase struct {
	ChatGateway  gateway.ChatGateway
	OpenAiClient *openai.Client
	Stream       chan ChatCompletionOuputDTO
}

func NewChatCompletionUseCase(chatGateway gateway.ChatGateway, openAiClient *openai.Client, stream chan ChatCompletionOuputDTO) *ChatCompletionUseCase {
	return &ChatCompletionUseCase{
		ChatGateway:  chatGateway,
		OpenAiClient: openAiClient,
		Stream:       stream,
	}
}

func (uc *ChatCompletionUseCase) Execute(ctx context.Context, input ChatCompletionInputDTO) (*ChatCompletionOuputDTO, error) {
	chat, err := uc.ChatGateway.FindChatByID(ctx, input.ChatID)
	if err != nil {
		if err.Error() == "chat not found" {
			// create new chat (entity)
			chat, err = createNewChat(input)
			if err != nil {
				return nil, errors.New("error creting new chat:" + err.Error())
			}
			// save on database
			err = uc.ChatGateway.CreateChat(ctx, chat)
			if err != nil {
				return nil, errors.New("error persisting new chat:" + err.Error())
			}
		} else {
			return nil, errors.New("error fetching existing chat: " + err.Error())
		}
	}
	userMessage, err := entity.NewMessage("user", input.UserMessage, chat.Config.Model)
	if err != nil {
		return nil, errors.New("error creating user message: " + err.Error())
	}
	err = chat.AddMessage(userMessage)
	if err != nil {
		return nil, errors.New("error adding new message: " + err.Error())
	}
	//Adiciona as mensagens na estrutura do opingAI
	messages := []openai.ChatCompletionMessage{}
	for _, msg := range chat.Messages {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	resp, err := uc.OpenAiClient.CreateChatCompletionStream(
		ctx,
		openai.ChatCompletionRequest{
			Model:            chat.Config.Model.Name,
			Messages:         messages,
			MaxTokens:        chat.Config.MaxTokens,
			Temperature:      chat.Config.Temperature,
			TopP:             chat.Config.TopP,
			PresencePenalty:  chat.Config.PresencePenalty,
			FrequencyPenalty: chat.Config.FrequencyPenalty,
			Stop:             chat.Config.Stop,
			Stream:           true,
		},
	)
	if err != nil {
		return nil, errors.New("error creating chat completion: " + err.Error())
	}

	//Recebe mensagens da openia via stream
	var fullResponse strings.Builder

	for {
		response, err := resp.Recv()
		// se a mensagem chegou ao fim sai do loop
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("error streaming response: " + err.Error())
		}
		// vai guardando resposta na variável fullResponse
		fullResponse.WriteString(response.Choices[0].Delta.Content)
		r := ChatCompletionOuputDTO{
			ChatID:  chat.ID,
			UserID:  input.UserID,
			Content: fullResponse.String(),
		}
		uc.Stream <- r
	}
	// Grava nova mensagems no banco de dados
	assistant, err := entity.NewMessage("assistant", fullResponse.String(), chat.Config.Model)
	if err != nil {
		return nil, errors.New("error creating assistant message: " + err.Error())
	}
	err = chat.AddMessage(assistant)
	if err != nil {
		return nil, errors.New("error adding new message: " + err.Error())
	}

	err = uc.ChatGateway.SaveChat(ctx, chat)
	if err != nil {
		return nil, errors.New("error saving chat in database: " + err.Error())
	}
	return &ChatCompletionOuputDTO{
		ChatID:  chat.ID,
		UserID:  input.ChatID,
		Content: fullResponse.String(),
	}, nil
}

func createNewChat(input ChatCompletionInputDTO) (*entity.Chat, error) {
	model := entity.NewModel(input.Config.Model, input.Config.ModelMaxTokens)
	chatConfig := &entity.ChatConfig{
		Temperature:      input.Config.Temperature,
		TopP:             input.Config.TopP,
		N:                input.Config.N,
		Stop:             input.Config.Stop,
		MaxTokens:        input.Config.MaxTokens,
		PresencePenalty:  input.Config.PresencePenalty,
		FrequencyPenalty: input.Config.PresencePenalty,
		Model:            model,
	}
	initialMessage, err := entity.NewMessage("system", input.Config.InitialSystemMessage, model)
	if err != nil {
		return nil, errors.New("error creating initial message: " + err.Error())
	}
	chat, err := entity.NewChat(input.UserID, initialMessage, chatConfig)
	if err != nil {
		return nil, errors.New("error creating new chat: " + err.Error())
	}

	return chat, nil
}
