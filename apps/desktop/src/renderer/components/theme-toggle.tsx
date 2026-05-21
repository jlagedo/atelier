import { Moon, Sun } from "@phosphor-icons/react";

import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useTheme } from "@/components/theme-provider";

export function ThemeToggle() {
  const { theme, toggleTheme } = useTheme();
  const next = theme === "dark" ? "light" : "dark";

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          onClick={toggleTheme}
          aria-label={`Switch to ${next} theme`}
        >
          {theme === "dark" ? <Sun weight="bold" /> : <Moon weight="bold" />}
        </Button>
      </TooltipTrigger>
      <TooltipContent side="bottom">Switch to {next} theme</TooltipContent>
    </Tooltip>
  );
}
