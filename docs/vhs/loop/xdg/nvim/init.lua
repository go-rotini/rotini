-- nvim for rotini-loop.tape only. Typed text lands exactly as written: no automatic indentation or
-- comment continuation, so the tape controls every space and tab.
vim.opt.termguicolors = true
vim.opt.background = "dark"
vim.cmd.colorscheme("retrobox")
vim.opt.number = true
vim.opt.swapfile = false
vim.opt.shada = ""
vim.opt.autoindent = false
vim.opt.smartindent = false
vim.opt.expandtab = false
vim.opt.tabstop = 4
vim.opt.shiftwidth = 4
vim.opt.wrap = false
vim.opt.hlsearch = false
vim.cmd("filetype indent off")
vim.api.nvim_create_autocmd("FileType", {
  pattern = "*",
  callback = function()
    vim.opt_local.indentexpr = ""
    vim.opt_local.autoindent = false
    vim.opt_local.formatoptions = ""
  end,
})
