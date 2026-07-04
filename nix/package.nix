{
  lib,
  buildGoModule,
  installShellFiles,
  # The flake's own source tree, passed in from flake.nix. Using `self` means
  # the build source is whatever git tracks (git+file / github inputs already
  # exclude gitignored junk like the root `sandbox` binary, dist/, node_modules).
  self,
}:

let
  version = "dev";
in
buildGoModule {
  pname = "sandbox";
  inherit version;

  src = self;

  # Bump this whenever go.mod/go.sum change. Set to lib.fakeHash, run the build,
  # and copy the "got:" hash Nix prints.
  vendorHash = "sha256-x0za8ykfofr9d2COVjluGVnk6t5aS9Nntv97UJZRqXg=";

  # Only build the host CLI. The repo also contains gen-eventschema and
  # tuikit-demo commands that are dev-only and not shipped.
  subPackages = [ "cmd/sandbox" ];

  nativeBuildInputs = [ installShellFiles ];

  ldflags = [
    "-s"
    "-w"
    "-X github.com/cullenmcdermott/sandbox/internal/cli.Version=${version}"
  ];

  # Tests reach for a live Kubernetes cluster / external infra (internal/e2e,
  # internal/k8sit), so don't run them as part of the package build.
  doCheck = false;

  postInstall = ''
    installShellCompletion --cmd sandbox \
      --bash <($out/bin/sandbox completion bash) \
      --zsh <($out/bin/sandbox completion zsh) \
      --fish <($out/bin/sandbox completion fish)
  '';

  meta = with lib; {
    description = "Run AI coding agents in remote Kubernetes sessions";
    license = licenses.mit;
    mainProgram = "sandbox";
    platforms = platforms.unix;
  };
}
