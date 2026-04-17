package providers

import (
	"fmt"
	"strings"
)

// PrintProvidersInfo prints information about available providers and models
func PrintProvidersInfo() {
	fmt.Println("Available Providers and Models:")
	fmt.Println(strings.Repeat("=", 40))
	
	models := ListAvailableModels()
	for provider, modelList := range models {
		fmt.Printf("\n%s:\n", strings.ToUpper(provider))
		for _, model := range modelList {
			fmt.Printf("  - %s\n", model)
		}
	}
	
	fmt.Println("\nProvider Detection:")
	fmt.Println("  - Models starting with 'claude-' → anthropic")
	fmt.Println("  - Models starting with 'gpt-' → openai")  
	fmt.Println("  - Models starting with 'code-' → openai")
	fmt.Println("  - Models containing 'davinci' → openai")
	fmt.Println("  - Models containing 'codex' → openai")
	fmt.Println("  - Use --provider flag to override detection")
	
	fmt.Println("\nEnvironment Variables:")
	fmt.Println("  ANTHROPIC_API_KEY - Required for Anthropic models")
	fmt.Println("  OPENAI_API_KEY    - Required for OpenAI models")
	fmt.Println("  STEPI_PROVIDER    - Default provider (anthropic/openai)")
	fmt.Println("  STEPI_MODEL       - Default model")
}