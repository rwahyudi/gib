# Fedora/Copr packaging for the gib source package.
#
# Regenerate the vendored source archive for each upstream release with:
#   spectool -g -R gib.spec
#   go_vendor_archive create -c go-vendor-tools.toml gib.spec
#   go_vendor_license -c go-vendor-tools.toml -C gib.spec report --update-spec --prompt --autofill=auto

%global goipath github.com/rwahyudi/gib
%global forgeurl https://github.com/rwahyudi/gib
%global gomodulesmode GO111MODULE=on

Version: 0.3.6
%global tag v%{version}
%gometa

%global common_description %{expand:
gib provides ib, an operator-focused command line interface for managing
Infoblox DNS records from the shell. It supports profile setup, encrypted
credentials, DNS view and zone context, record workflows, cache inspection,
next available IP lookup, and shell completion.}

%global golicenses LICENSE
%global godocs README.md docs/performance-caching.md docs/licensing.md
%{!?bash_completions_dir:%global bash_completions_dir %{_datadir}/bash-completion/completions}

Name:           gib
Release:        1%{?dist}
Summary:        Operator-focused Infoblox DNS CLI
License:        Apache-2.0 AND BSD-3-Clause AND MIT
URL:            %{gourl}
Source0:        %{gosource}
Source1:        %{archivename}-vendor.tar.bz2
Source2:        go-vendor-tools.toml

BuildRequires:  bash-completion
BuildRequires:  go-rpm-macros
BuildRequires:  go-vendor-tools
BuildRequires:  golang >= 1.24

Requires:       ca-certificates

%description
%{common_description}

%generate_buildrequires
%go_vendor_license_buildrequires -c %{S:2}

%prep
%autosetup -p1 %{forgesetupargs} -a1
%goprep -k -e

%build
export CGO_ENABLED=0
%gobuild -o %{gobuilddir}/bin/ib %{goipath}/cmd/ib

%install
%go_vendor_license_install -c %{S:2}

install -m 0755 -vd %{buildroot}%{_bindir}
install -m 0755 -vp %{gobuilddir}/bin/ib %{buildroot}%{_bindir}/ib

install -m 0755 -vd %{buildroot}%{bash_completions_dir}
install -m 0644 -vp packaging/bash_completion/ib %{buildroot}%{bash_completions_dir}/ib

install -m 0755 -vd %{buildroot}%{_mandir}/man1
install -m 0644 -vp packaging/man/ib.1 %{buildroot}%{_mandir}/man1/ib.1

%check
%go_vendor_license_check -c %{S:2}
export CGO_ENABLED=0
%gocheck2

%files -f %{go_vendor_license_filelist}
%doc %{godocs}
%{_bindir}/ib
%{bash_completions_dir}/ib
%{_mandir}/man1/ib.1*

%changelog
* Wed Jun 10 2026 rwahyudi <rwahyudi@users.noreply.github.com> - 0.3.6-1
- Release 0.3.6

* Sun May 24 2026 rwahyudi <rwahyudi@users.noreply.github.com> - 0.3.5-1
- Release 0.3.5

* Fri May 22 2026 rwahyudi <rwahyudi@users.noreply.github.com> - 0.3.4-1
- Release 0.3.4

* Fri May 22 2026 rwahyudi <rwahyudi@users.noreply.github.com> - 0.3.3-1
- Release 0.3.3

* Fri May 15 2026 rwahyudi <rwahyudi@users.noreply.github.com> - 0.3.0-1
- Initial package for Copr and EPEL review
