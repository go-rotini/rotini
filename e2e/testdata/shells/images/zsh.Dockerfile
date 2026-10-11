FROM zshusers/zsh:5.9@sha256:fbf7d12732ea085564d6900c40d044f9860e495f905018d34b5e1c2e1fbf878b
RUN groupadd rotinigroup && useradd -M -g rotinigroup rotiniuser
